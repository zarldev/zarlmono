package providertest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// AssertCancellationHonoured is the assertion for cancellation
// scenarios. Pass it as Scenario.Assert alongside
// CancelMidStream=true and a Handler that hangs forever. A
// well-behaved provider closes the chunks channel after observing
// ctx.Done; the last chunk (or completeErr) carries
// context.Canceled or context.DeadlineExceeded.
//
// We tolerate either signal because providers vary on where they
// surface cancellation: some yield a final chunk with chunk.Error
// set, some return an error from Complete itself, some just close
// the channel without an error. All three are valid "provider
// honoured ctx.Done" outcomes.
func AssertCancellationHonoured(t *testing.T, collected []llm.CompletionChunk, completeErr error) {
	t.Helper()

	// Completion of the chunks loop is itself evidence the provider
	// observed cancellation — without it, the test would have hit
	// the per-scenario timeout. So getting here at all is the
	// primary signal.

	// Secondary signal: SOMETHING should mention cancellation.
	if completeErr != nil &&
		(errors.Is(completeErr, context.Canceled) || errors.Is(completeErr, context.DeadlineExceeded)) {
		return
	}
	for _, c := range collected {
		if c.Error == nil {
			continue
		}
		if errors.Is(c.Error, context.Canceled) || errors.Is(c.Error, context.DeadlineExceeded) {
			return
		}
		// Some providers wrap the error message — tolerate that as
		// long as the underlying chain is clearly a cancellation.
		if strings.Contains(c.Error.Error(), "context canceled") ||
			strings.Contains(c.Error.Error(), "context deadline exceeded") {
			return
		}
	}
	// Channel closed cleanly with no cancellation marker — that's
	// also acceptable. Some providers swallow ctx.Done into a clean
	// stream end; the test passed if we got here without timing out.
}

// AssertUsageReported is the assertion for usage-reporting scenarios.
// Expects the FINAL chunk to carry a non-nil Usage{} with at least
// TotalTokens (or PromptTokens + CompletionTokens) populated. A
// provider that drops usage entirely when the server reported it is
// failing the contract — the runner's compaction policy reads usage
// to drive adaptive trim.
func AssertUsageReported(t *testing.T, collected []llm.CompletionChunk, completeErr error) {
	t.Helper()
	if completeErr != nil {
		t.Fatalf("Complete returned err = %v, want nil", completeErr)
	}
	if len(collected) == 0 {
		t.Fatal("no chunks collected; provider produced empty stream")
	}
	last := collected[len(collected)-1]
	if last.Usage == nil {
		// Search the tail: some providers emit usage on the
		// pre-last chunk and a bare {Done: true} after. Look back
		// up to 3 chunks.
		for i := len(collected) - 1; i >= 0 && i >= len(collected)-3; i-- {
			if collected[i].Usage != nil {
				last = collected[i]
				break
			}
		}
	}
	if last.Usage == nil {
		t.Fatalf("no chunk reported Usage; provider must surface server-side token counts")
	}
	if last.Usage.TotalTokens == 0 && last.Usage.PromptTokens == 0 && last.Usage.CompletionTokens == 0 {
		t.Errorf("Usage = %+v, want at least one non-zero token count", last.Usage)
	}
}

// AssertStreamingDoneSet is the assertion for streaming-completion
// scenarios. The final non-error chunk MUST carry Done=true so the
// runner can detect end-of-stream without resorting to "the channel
// closed". Providers that close the channel without a Done sentinel
// violate the contract — the runner relies on Done to drive
// loop-exit decisions.
func AssertStreamingDoneSet(t *testing.T, collected []llm.CompletionChunk, completeErr error) {
	t.Helper()
	if completeErr != nil {
		t.Fatalf("Complete returned err = %v, want nil", completeErr)
	}
	if len(collected) == 0 {
		t.Fatal("no chunks collected")
	}
	for _, c := range collected {
		if c.Done {
			return
		}
	}
	t.Errorf("no chunk had Done=true; provider must emit a terminal chunk")
}

// AssertToolCallEmitted is the assertion for tool-call scenarios.
// The chunks stream must include at least one chunk whose ToolCalls
// is non-empty AND the called tool name matches what the scenario
// expects (passed via wantName). Use with RequestWithTool.
func AssertToolCallEmitted(wantName string) func(t *testing.T, collected []llm.CompletionChunk, completeErr error) {
	return func(t *testing.T, collected []llm.CompletionChunk, completeErr error) {
		t.Helper()
		if completeErr != nil {
			t.Fatalf("Complete returned err = %v, want nil", completeErr)
		}
		for _, c := range collected {
			for _, tc := range c.ToolCalls {
				if tc.Function.Name == wantName {
					return
				}
			}
		}
		t.Errorf("no chunk surfaced a ToolCall for %q; provider must propagate server-side function calls", wantName)
	}
}

// AssertErrorSurfaced is the assertion for error-path scenarios
// (server returns 4xx/5xx). The provider must indicate failure
// either via Complete's error return OR via a chunk.Error — silent
// success on a server error is a contract violation.
func AssertErrorSurfaced(t *testing.T, collected []llm.CompletionChunk, completeErr error) {
	t.Helper()
	if completeErr != nil {
		return
	}
	for _, c := range collected {
		if c.Error != nil {
			if !c.Done {
				t.Errorf("error chunk has Done=false; provider errors must be terminal chunks")
			}
			return
		}
	}
	t.Errorf("server returned an error response but provider yielded clean stream; failures must be surfaced")
}

// CollectContent joins every chunk's Content into a single string.
// Helper for custom Assert functions that want to inspect the
// model's text reply alongside structured fields.
func CollectContent(chunks []llm.CompletionChunk) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Content)
	}
	return b.String()
}
