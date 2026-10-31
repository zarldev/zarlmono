package refine_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
	"github.com/zarldev/zarlmono/swebench-eval/refine"
	"github.com/zarldev/zarlmono/swebench-eval/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type roundClient struct {
	t          *testing.T
	calls      int
	noChangeAt int
}

func (c *roundClient) Complete(_ context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		c.calls++
		if len(request.Tools) != 0 {
			c.t.Error("improver tools advertised")
		}
		input := request.Messages[1].Content
		if strings.Contains(input, "CANARY") || strings.Contains(input, "attempt_verdicts") || strings.Contains(input, "resolved") {
			c.t.Error("scoring/private result reached improver")
		}
		var payload struct {
			Labels   []string               `json:"labels"`
			Evidence []harness.EvidenceView `json:"evidence"`
		}
		if err := json.Unmarshal([]byte(input), &payload); err != nil {
			c.t.Fatal(err)
		}
		if c.calls == 2 && len(payload.Evidence) != 3 {
			c.t.Error("composition missing contrasts")
		}
		var candidates []map[string]string
		for _, label := range payload.Labels {
			candidates = append(candidates, map[string]string{"label": label, "guidance": "Use focused evidence for " + label})
		}
		var experiment any = map[string]any{"version": 1, "repeats": 1, "candidates": candidates}
		noChange := c.calls == c.noChangeAt
		if noChange {
			experiment = nil
		}
		response, err := json.Marshal(map[string]any{"version": 1, "no_change": noChange, "rationale": "Supported by supplied metadata.", "evidence_ids": []string{payload.Evidence[0].ID}, "experiment": experiment})
		if err != nil {
			c.t.Fatal(err)
		}
		yield(llm.CompletionChunk{Content: string(response)}, nil)
	}
}

func probeResult(trial harness.Trial) refine.ProbeResult {
	return refine.ProbeResult{
		Record: runner.TaskResult{InstanceID: "task", DriverName: trial.DriverName(), Resolved: new(true), Result: harness.Result{
			Diff: "CANARY_PATCH", Verified: true, HarnessLog: "CANARY_LOG", AttemptVerdicts: []harness.AttemptVerdict{{Error: "CANARY_VERDICT"}},
			Evidence: harness.EvidenceReceipt{File: "artifact", SHA256: "digest", RunID: "run", TaskID: "task", Driver: trial.DriverName()},
		}},
		Evidence: harness.EvidenceView{ID: trial.DriverName(), TaskPrompt: "allowed task", TerminalReason: "completed"},
	}
}

func TestRoundSerialTopologyAndNoChange(t *testing.T) {
	for _, noChangeAt := range []int{0, 1, 2} {
		t.Run(map[int]string{0: "full", 1: "proposal-no-change", 2: "composition-no-change"}[noChangeAt], func(t *testing.T) {
			client := &roundClient{t: t, noChangeAt: noChangeAt}
			var probes []string
			var events []refine.Event
			round, err := refine.Run(t.Context(), func(_ context.Context, trial harness.Trial) (refine.ProbeResult, error) {
				probes = append(probes, trial.DriverName())
				return probeResult(trial), nil
			}, harness.NewImprover(client, time.Minute), func(event refine.Event) error { events = append(events, event); return nil })
			if err != nil {
				t.Fatal(err)
			}
			wantProbes, wantCalls := 4, 2
			if noChangeAt == 1 {
				wantProbes, wantCalls = 1, 1
			}
			if noChangeAt == 2 {
				wantProbes = 3
			}
			if !round.Frozen || round.NoChange != (noChangeAt != 0) || len(round.Records) != wantProbes || client.calls != wantCalls || !slices.Equal(probes, refine.PlannedDrivers()[:wantProbes]) {
				t.Fatalf("round=%+v probes=%v calls=%d", round, probes, client.calls)
			}
			last := events[len(events)-1]
			if last.State != "frozen" || len(last.Skipped) != 4-wantProbes {
				t.Fatalf("skip/freeze journal: %+v", last)
			}
			for i, event := range events[:len(events)-1] {
				if i%2 == 0 && event.State != "started" {
					t.Fatal("stage did not start before external work")
				}
				if i%2 == 1 && event.State != "completed" {
					t.Fatal("stage did not settle")
				}
			}
		})
	}
}

func TestRoundStopsOnProbeOrJournalFailure(t *testing.T) {
	boom := errors.New("probe failure")
	for failAt := range 4 {
		t.Run(refine.PlannedDrivers()[failAt], func(t *testing.T) {
			client := &roundClient{t: t}
			calls := 0
			var events []refine.Event
			round, err := refine.Run(t.Context(), func(_ context.Context, trial harness.Trial) (refine.ProbeResult, error) {
				result := probeResult(trial)
				calls++
				if calls == failAt+1 {
					result.Record.Result.Err = boom
					return result, boom
				}
				return result, nil
			}, harness.NewImprover(client, time.Minute), func(event refine.Event) error { events = append(events, event); return nil })
			if !errors.Is(err, boom) || round.Frozen || len(round.Records) != failAt+1 || calls != failAt+1 {
				t.Fatalf("partial round: %+v err=%v", round, err)
			}
			last := events[len(events)-1]
			if last.State != "failed" || last.Receipt.File != "artifact" || last.Receipt.SHA256 != "digest" {
				t.Fatalf("failed receipt lost: %+v", last)
			}
		})
	}
	client := &roundClient{t: t}
	journalErr := errors.New("journal unavailable")
	round, err := refine.Run(t.Context(), func(_ context.Context, trial harness.Trial) (refine.ProbeResult, error) {
		return probeResult(trial), nil
	}, harness.NewImprover(client, time.Minute), func(event refine.Event) error {
		if event.State == "completed" {
			return journalErr
		}
		return nil
	})
	if !errors.Is(err, journalErr) || round.Frozen || len(round.Records) != 1 || client.calls != 0 {
		t.Fatalf("journal failure: %+v %v", round, err)
	}
}

func TestRoundCancellationAndIncompleteArtifact(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &roundClient{t: t}
	round, err := refine.Run(ctx, func(_ context.Context, trial harness.Trial) (refine.ProbeResult, error) {
		cancel()
		return probeResult(trial), nil
	}, harness.NewImprover(client, time.Minute), func(refine.Event) error { return nil })
	if !errors.Is(err, context.Canceled) || round.Frozen || client.calls != 0 {
		t.Fatalf("cancel: %+v %v", round, err)
	}
	var last refine.Event
	_, err = refine.Run(t.Context(), func(_ context.Context, trial harness.Trial) (refine.ProbeResult, error) {
		result := probeResult(trial)
		result.Record.Result.Evidence = harness.EvidenceReceipt{}
		result.Record.Result.EvidenceFile = "partial.jsonl"
		return result, harness.ErrEvidence
	}, harness.NewImprover(client, time.Minute), func(event refine.Event) error { last = event; return nil })
	if !errors.Is(err, harness.ErrEvidence) || last.IncompleteArtifact != "partial.jsonl" || last.Receipt.File != "" {
		t.Fatalf("incomplete artifact mislabeled: %+v %v", last, err)
	}
}

// The wrapped scripted client keeps the same successful decision contract while
// allowing either model stage to fail without constructing a malformed decision.
type failingRoundClient struct {
	client        *roundClient
	calls, failAt int
	err           error
}

func (c *failingRoundClient) Complete(ctx context.Context, request llm.CompletionRequest) llm.CompletionStream {
	return func(yield func(llm.CompletionChunk, error) bool) {
		c.calls++
		if c.calls == c.failAt {
			if !yield(llm.CompletionChunk{Content: "partial response"}, nil) {
				return
			}
			yield(llm.CompletionChunk{}, c.err)
			return
		}
		for chunk, err := range c.client.Complete(ctx, request) {
			if !yield(chunk, err) {
				return
			}
		}
	}
}

func TestRoundStopsAtEitherImproverFailure(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(map[int]string{1: "propose", 2: "compose"}[failAt], func(t *testing.T) {
			boom := errors.New("improver transport failure")
			client := &failingRoundClient{client: &roundClient{t: t}, failAt: failAt, err: boom}
			probes := 0
			var last refine.Event
			round, err := refine.Run(t.Context(), func(_ context.Context, trial harness.Trial) (refine.ProbeResult, error) {
				probes++
				return probeResult(trial), nil
			}, harness.NewImprover(client, time.Minute), func(event refine.Event) error { last = event; return nil })
			if !errors.Is(err, boom) || round.Frozen || probes != 2*failAt-1 || client.calls != failAt {
				t.Fatalf("model failure advanced round: %+v %v probes=%d calls=%d", round, err, probes, client.calls)
			}
			if last.State != "failed" || string(last.Improvement.Response) != "partial response" {
				t.Fatalf("partial model output lost: %+v", last)
			}
		})
	}
}
