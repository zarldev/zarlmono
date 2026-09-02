// Binary hitl_resume demonstrates an application-managed pause and resume around
// workflow, checkpoint, and HITL primitives without an LLM.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/checkpoint"
	"github.com/zarldev/zarlmono/zkit/agent/hitl"
	"github.com/zarldev/zarlmono/zkit/agent/workflow"
)

const (
	runID        = "deploy-42"
	checkpointID = checkpoint.ID("deploy-42-before-review")
	requestID    = hitl.RequestID("deploy-42-review")
)

var exampleTime = time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)

type deployment struct {
	Service string
	Target  string
}

type reviewBoundary struct {
	Request hitl.Request
}

type result struct {
	Status string
	Target string
}

func main() {
	decision := flag.String("decision", "", "human decision: approve, deny, or edit; omit to prompt")
	flag.Parse()
	if err := run(context.Background(), os.Stdin, os.Stdout, *decision); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdin io.Reader, stdout io.Writer, decision string) error {
	store := checkpoint.NewMemoryStore()
	boundary, err := runToReview(ctx, store)
	if err != nil {
		return err
	}

	_, decided, err := (hitl.ApproveLowRisk{}).Review(ctx, boundary.Request)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "paused request=%s risk=%s decided=%t checkpoint=%s\n",
		boundary.Request.ID, boundary.Request.Risk, decided, boundary.Request.CheckpointID)

	if decision == "" {
		fmt.Fprintln(stdout, "awaiting human decision [approve|deny|edit]")
		decision, err = readDecision(stdin)
		if err != nil {
			return err
		}
	}
	review, err := humanReview(boundary.Request, decision)
	if err != nil {
		return err
	}
	cp, err := store.Load(ctx, checkpointID)
	if err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	fmt.Fprintf(stdout, "loaded checkpoint=%s step=%s decision=%s\n", cp.ID, cp.Step, review.Decision)

	out, err := continueAfterReview(ctx, cp, review)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "result status=%s target=%s\n", out.Status, out.Target)
	return nil
}

func runToReview(ctx context.Context, store checkpoint.Store) (reviewBoundary, error) {
	graph := workflow.NewGraph()
	if err := workflow.AddNode(graph, "prepare", workflow.NodeFunc[deployment, reviewBoundary](func(ctx context.Context, deploy deployment) (reviewBoundary, error) {
		cp := checkpoint.Checkpoint{
			ID: checkpointID, RunID: runID, Step: "before-production-deploy", CreatedAt: exampleTime,
			State: map[string]any{"service": deploy.Service, "target": deploy.Target},
		}
		if err := store.Save(ctx, cp); err != nil {
			return reviewBoundary{}, err
		}
		return reviewBoundary{Request: hitl.Request{
			ID: requestID, RunID: runID, CheckpointID: string(cp.ID),
			Action: "deploy", Summary: "Deploy billing-api to production",
			Payload: map[string]any{"service": deploy.Service, "target": deploy.Target},
			Risk:    hitl.RiskHigh, CreatedAt: exampleTime,
		}}, nil
	})); err != nil {
		return reviewBoundary{}, err
	}
	if err := graph.AddEdge(workflow.Start.String(), "prepare"); err != nil {
		return reviewBoundary{}, err
	}
	if err := graph.AddEdge("prepare", workflow.End.String()); err != nil {
		return reviewBoundary{}, err
	}
	runnable, err := graph.Compile()
	if err != nil {
		return reviewBoundary{}, err
	}
	out, err := runnable.Invoke(ctx, deployment{Service: "billing-api", Target: "production"})
	if err != nil {
		return reviewBoundary{}, err
	}
	boundary, ok := out.(reviewBoundary)
	if !ok {
		return reviewBoundary{}, fmt.Errorf("review boundary: unexpected output %T", out)
	}
	return boundary, nil
}

func humanReview(req hitl.Request, decision string) (hitl.Review, error) {
	review := hitl.Review{RequestID: req.ID, Reviewer: "operator@example", CreatedAt: exampleTime}
	switch decision {
	case "approve":
		review.Decision = hitl.DecisionApprove
	case "deny":
		review.Decision = hitl.DecisionDeny
		review.Comment = "change window closed"
	case "edit":
		review.Decision = hitl.DecisionEdit
		review.Comment = "use staging first"
		review.Patch = map[string]any{"target": "staging"}
	default:
		return hitl.Review{}, fmt.Errorf("review: unknown decision %q", decision)
	}
	return review, nil
}

func readDecision(stdin io.Reader) (string, error) {
	decision, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read human decision: %w", err)
	}
	decision = strings.TrimSpace(decision)
	if decision == "" {
		return "", errors.New("read human decision: no decision provided")
	}
	return decision, nil
}

// continueAfterReview is application code: workflow does not discover or resume
// this function automatically. The application loads a checkpoint and calls it.
func continueAfterReview(ctx context.Context, cp checkpoint.Checkpoint, review hitl.Review) (result, error) {
	graph := workflow.NewGraph()
	if err := workflow.AddNode(graph, "apply-review", workflow.NodeFunc[checkpoint.Checkpoint, result](func(_ context.Context, saved checkpoint.Checkpoint) (result, error) {
		target, ok := saved.State["target"].(string)
		if !ok {
			return result{}, errors.New("continuation: checkpoint target is not a string")
		}
		switch review.Decision {
		case hitl.DecisionApprove:
			return result{Status: "deployed", Target: target}, nil
		case hitl.DecisionDeny:
			return result{Status: "cancelled", Target: target}, nil
		case hitl.DecisionEdit:
			edited, ok := review.Patch["target"].(string)
			if !ok {
				return result{}, errors.New("continuation: edited target is not a string")
			}
			return result{Status: "deployed", Target: edited}, nil
		default:
			return result{}, fmt.Errorf("continuation: unsupported decision %q", review.Decision)
		}
	})); err != nil {
		return result{}, err
	}
	if err := graph.AddEdge(workflow.Start.String(), "apply-review"); err != nil {
		return result{}, err
	}
	if err := graph.AddEdge("apply-review", workflow.End.String()); err != nil {
		return result{}, err
	}
	runnable, err := graph.Compile()
	if err != nil {
		return result{}, err
	}
	out, err := runnable.Invoke(ctx, cp)
	if err != nil {
		return result{}, err
	}
	res, ok := out.(result)
	if !ok {
		return result{}, fmt.Errorf("continuation: unexpected output %T", out)
	}
	return res, nil
}
