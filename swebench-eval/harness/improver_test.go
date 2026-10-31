package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type improvementClient func(context.Context, llm.CompletionRequest) llm.CompletionStream

func (f improvementClient) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return f(ctx, request)
}

const proposalReply = `{"version":1,"no_change":false,"rationale":"Reduce repeated actions.","evidence_ids":["baseline"],"experiment":{"version":1,"repeats":1,"candidates":[{"label":"general","guidance":"Read narrowly."},{"label":"domain","guidance":"Follow the task contract."}]}}`
const unchangedReply = `{"version":1,"no_change":true,"rationale":"Metadata does not support a change.","evidence_ids":["baseline"],"experiment":null}`

func TestImproverHasNoToolsAndPreservesUsageAbsence(t *testing.T) {
	for _, reported := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "reported-zero"}[reported], func(t *testing.T) {
			client := improvementClient(func(_ context.Context, request llm.CompletionRequest) llm.CompletionStream {
				if len(request.Tools) != 0 || request.MaxTokens != harness.ImproverOutputTokens {
					t.Fatalf("unexpected tools/budget: %+v", request)
				}
				if !strings.Contains(request.Messages[1].Content, `"id":"baseline"`) {
					t.Fatal("evidence identity missing")
				}
				return func(yield func(llm.CompletionChunk, error) bool) {
					yield(llm.CompletionChunk{Content: proposalReply, UsageReported: reported}, nil)
				}
			})
			improver := harness.NewImprover(client, time.Minute)
			out, err := improver.Propose(t.Context(), harness.EvidenceView{ID: "baseline", TaskPrompt: "task"})
			if err != nil {
				t.Fatal(err)
			}
			if out.Decision.NoChange || len(out.Decision.Experiment.Trials()) != 3 || string(out.Response) != proposalReply || len(out.Input) == 0 {
				t.Fatalf("proposal: %+v", out)
			}
			if (out.Usage != nil) != reported {
				t.Fatal("missing usage confused with reported zero")
			}
		})
	}
}

func TestImproverRejectsUnsafeOrOversizedResponses(t *testing.T) {
	boom := errors.New("transport stopped")
	for _, name := range []string{"tools", "oversized", "malformed", "invalid-utf8", "stream-error", "request-limit", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			client := improvementClient(func(_ context.Context, _ llm.CompletionRequest) llm.CompletionStream {
				calls++
				return func(yield func(llm.CompletionChunk, error) bool) {
					switch name {
					case "tools":
						if yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{ID: "illegal"}}}, nil) {
							t.Error("tool-call stream not stopped")
						}
					case "oversized":
						if yield(llm.CompletionChunk{Content: strings.Repeat("x", harness.ImproverResponseLimit+1)}, nil) {
							t.Error("oversized stream not stopped")
						}
					case "invalid-utf8":
						yield(llm.CompletionChunk{Content: "\xff"}, nil)
					case "stream-error":
						yield(llm.CompletionChunk{}, boom)
					default:
						yield(llm.CompletionChunk{Content: "not-json"}, nil)
					}
				}
			})
			view := harness.EvidenceView{ID: "baseline"}
			if name == "request-limit" {
				view.TaskPrompt = strings.Repeat("x", harness.ImproverRequestLimit)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			out, err := harness.NewImprover(client, time.Minute).Propose(ctx, view)
			if name == "cancelled" {
				if !errors.Is(err, context.Canceled) || calls != 0 {
					t.Fatalf("cancel: %v calls=%d", err, calls)
				}
				return
			}
			if !errors.Is(err, harness.ErrImprovement) {
				t.Fatalf("error=%v", err)
			}
			if name == "request-limit" && calls != 0 {
				t.Fatal("oversized request reached provider")
			}
			if name == "stream-error" && !errors.Is(err, boom) {
				t.Fatal("lost transport cause")
			}
			if name == "invalid-utf8" {
				encoded, err := json.Marshal(out)
				if err != nil {
					t.Fatal(err)
				}
				var decoded harness.Improvement
				if err = json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatal(err)
				}
				if string(decoded.Response) != "\xff" {
					t.Fatal("raw invalid response repaired")
				}
			}
		})
	}
}

func TestImproverDeadlineStopsTheStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := improvementClient(func(ctx context.Context, _ llm.CompletionRequest) llm.CompletionStream {
			return func(yield func(llm.CompletionChunk, error) bool) {
				<-ctx.Done()
				yield(llm.CompletionChunk{}, ctx.Err())
			}
		})
		_, err := harness.NewImprover(client, time.Second).Propose(t.Context(), harness.EvidenceView{ID: "baseline"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
}

func TestGuidanceDecisionBoundaries(t *testing.T) {
	views := []harness.EvidenceView{{ID: "baseline"}}
	decision, err := harness.ParseGuidanceDecision([]byte(unchangedReply), []string{"general", "domain"}, views)
	if err != nil || !decision.NoChange || len(decision.Experiment.Trials()) != 0 {
		t.Fatalf("no change: %+v %v", decision, err)
	}
	for name, data := range map[string]string{
		"forged citation":    strings.Replace(proposalReply, `["baseline"]`, `["made-up"]`, 1),
		"duplicate citation": strings.Replace(proposalReply, `["baseline"]`, `["baseline","baseline"]`, 1),
		"wrong labels":       strings.Replace(proposalReply, `"general"`, `"other"`, 1),
		"extra repetitions":  strings.Replace(proposalReply, `"repeats":1`, `"repeats":2`, 1),
		"duplicate field":    strings.Replace(proposalReply, `"version":1`, `"version":1,"version":1`, 1),
		"case-folded":        strings.Replace(proposalReply, `"rationale"`, `"Rationale"`, 1),
		"implicit boolean":   strings.Replace(proposalReply, `"no_change":false,`, ``, 1),
		"null boolean":       strings.Replace(proposalReply, `"no_change":false`, `"no_change":null`, 1),
		"contradictory noop": strings.Replace(proposalReply, `"no_change":false`, `"no_change":true`, 1),
		"extra score":        strings.Replace(proposalReply, `"version":1`, `"version":1,"score":1`, 1),
		"unicode":            strings.Replace(proposalReply, "Reduce", `\ud800`, 1),
		"fenced":             "```json\n" + proposalReply + "\n```",
		"oversized":          strings.Repeat(" ", harness.ImproverResponseLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := harness.ParseGuidanceDecision([]byte(data), []string{"general", "domain"}, views); !errors.Is(err, harness.ErrExperiment) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
