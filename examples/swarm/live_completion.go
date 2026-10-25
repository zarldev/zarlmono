package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/agent/board"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

var errLiveVerified = errors.New("live goal independently verified")

// The meter lock owns request admission and the final local tool transition.
// Lock order is meter -> artifact/swarm/board. Provider I/O never holds it.
type completionTool struct {
	tools.Tool
	meter    *liveMeter
	artifact *RegexArtifact
	member   swarm.Member
}

func (t completionTool) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	t.meter.mu.Lock()
	defer t.meter.mu.Unlock()
	if t.meter.verified {
		return nil, errLiveVerified
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	result, err := t.Tool.Execute(ctx, call)
	if err != nil || !result.Success {
		return result, err
	}
	if name := t.Definition().Name; name != "board_post" && name != "candidate_review" {
		return result, nil
	}
	candidate := t.artifact.Snapshot()
	if !candidate.Approved || candidate.Version <= 1 || candidate.ReviewedVersion != candidate.Version {
		return result, nil
	}
	page, err := t.member.List(ctx, 0, board.MaxPage, "")
	if err != nil {
		return result, err
	}
	if !liveChain(page.Posts) {
		return result, nil
	}
	oracle := t.artifact.Test()
	if oracle.Total > 0 && oracle.Total == oracle.Passed && len(t.meter.failures) == 0 && context.Cause(ctx) == nil {
		// No competing tool can mutate the accepted artifact, and no new request
		// can be admitted between verification and cancellation. Never wait for
		// this worker here: RunLiveExperiment owns Close and the final join.
		t.meter.verified = true
		t.meter.finish(errLiveVerified)
	}
	return result, nil
}

func protectLiveTools(registry *tools.Registry, meter *liveMeter, artifact *RegexArtifact, member swarm.Member) error {
	for _, spec := range registry.ToolSpecs() {
		tool, _ := registry.Tool(spec.Name)
		if err := registry.Register(completionTool{Tool: tool, meter: meter, artifact: artifact, member: member}); err != nil {
			return fmt.Errorf("wrap live tool: %w", err)
		}
	}
	return nil
}

func liveChain(posts []board.Summary) bool {
	var previous board.ID
	for _, step := range []struct{ author, topic string }{{"host", "task"}, {"agent/researcher", "research"}, {"agent/implementer", "implementation"}, {"agent/reviewer", "review"}} {
		found := false
		for _, post := range posts {
			if string(post.Author) == step.author && post.Topic == step.topic && post.ReplyTo == previous {
				previous, found = post.ID, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Require every error leaf to be cancellation fallout; errors.Join must not
// hide a real provider or budget failure merely because it also wraps cancellation.
func onlyVerifiedCancellation(err error) bool {
	switch err := err.(type) { //nolint:errorlint // Inspect direct unwrap edges; errors.As would skip branches of the error tree.
	case interface{ Unwrap() []error }:
		children := err.Unwrap()
		for _, child := range children {
			if !onlyVerifiedCancellation(child) {
				return false
			}
		}
		return len(children) > 0
	case interface{ Unwrap() error }:
		return onlyVerifiedCancellation(err.Unwrap())
	default:
		return errors.Is(err, context.Canceled) || errors.Is(err, runner.ErrCancelled) || errors.Is(err, errLiveVerified)
	}
}

func (m *liveMeter) recordFailure(ctx context.Context, err error) {
	if err == nil || (errors.Is(context.Cause(ctx), errLiveVerified) && onlyVerifiedCancellation(err)) {
		return
	}
	m.mu.Lock()
	m.failures = append(m.failures, err)
	m.mu.Unlock()
}
