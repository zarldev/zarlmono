package providertest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// AssertCancellationHonoured requires cancellation to surface as the stream's
// single terminal sequence error. Cancellation is not a successful EOF and is
// never encoded in a CompletionChunk.
func AssertCancellationHonoured(t *testing.T, collected []llm.CompletionChunk, streamErr error) {
	t.Helper()
	if streamErr == nil {
		t.Fatal("stream completed successfully after cancellation; want terminal cancellation error")
	}
	if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
		return
	}
	if strings.Contains(streamErr.Error(), "context canceled") ||
		strings.Contains(streamErr.Error(), "context deadline exceeded") {
		return
	}
	t.Errorf("terminal error = %v, want context cancellation", streamErr)
}

// AssertSuccessfulCompletion requires normal EOF with no terminal sequence
// error. Successful completion needs no lifecycle sentinel or metadata chunk.
func AssertSuccessfulCompletion(t *testing.T, _ []llm.CompletionChunk, streamErr error) {
	t.Helper()
	if streamErr != nil {
		t.Errorf("terminal stream error = %v, want successful EOF", streamErr)
	}
}

// AssertStreamingEOF requires successful completion by normal iterator return.
// Streaming completion has no terminal lifecycle chunk.
func AssertStreamingEOF(t *testing.T, collected []llm.CompletionChunk, streamErr error) {
	t.Helper()
	AssertSuccessfulCompletion(t, collected, streamErr)
}

// AssertUsageReported requires successful EOF and at least one observation with
// explicit usage presence. Usage is an owned value, and an all-zero reported
// value is valid and distinct from absent usage.
func AssertUsageReported(t *testing.T, collected []llm.CompletionChunk, streamErr error) {
	t.Helper()
	AssertSuccessfulCompletion(t, collected, streamErr)
	for _, chunk := range collected {
		if chunk.UsageReported {
			return
		}
	}
	t.Error("no chunk had UsageReported=true; provider must surface server-reported usage")
}

// AssertFinishReasonReported requires successful EOF and at least one semantic,
// recognized, non-unknown finish reason.
func AssertFinishReasonReported(t *testing.T, collected []llm.CompletionChunk, streamErr error) {
	t.Helper()
	AssertSuccessfulCompletion(t, collected, streamErr)
	for _, chunk := range collected {
		if chunk.FinishReason.IsValid() && chunk.FinishReason != llm.FinishReasons.UNKNOWN {
			return
		}
	}
	t.Error("no chunk reported a recognized semantic FinishReason")
}

// AssertFinishReason returns an assertion requiring the requested semantic
// finish reason on at least one observation before successful EOF.
func AssertFinishReason(want llm.FinishReason) Assertion {
	return func(t *testing.T, collected []llm.CompletionChunk, streamErr error) {
		t.Helper()
		AssertSuccessfulCompletion(t, collected, streamErr)
		for _, chunk := range collected {
			if chunk.FinishReason == want {
				return
			}
		}
		t.Errorf("no chunk reported FinishReason %q", want)
	}
}

// AssertToolCallEmitted returns an assertion requiring a call to wantName before
// successful EOF.
func AssertToolCallEmitted(wantName string) Assertion {
	return func(t *testing.T, collected []llm.CompletionChunk, streamErr error) {
		t.Helper()
		AssertSuccessfulCompletion(t, collected, streamErr)
		for _, chunk := range collected {
			for _, call := range chunk.ToolCalls {
				if call.Function.Name == wantName {
					return
				}
			}
		}
		t.Errorf("no chunk surfaced a ToolCall for %q", wantName)
	}
}

// AssertErrorSurfaced requires a terminal sequence error. The harness separately
// verifies that it was yielded with a zero chunk and that no observation followed
// it.
func AssertErrorSurfaced(t *testing.T, _ []llm.CompletionChunk, streamErr error) {
	t.Helper()
	if streamErr == nil {
		t.Error("stream completed successfully; want terminal error")
	}
}

// AssertStoppedByConsumer requires a clean return after the harness returns
// false from yield. Use it with Scenario.StopAfter greater than zero.
func AssertStoppedByConsumer(t *testing.T, _ []llm.CompletionChunk, streamErr error) {
	t.Helper()
	if streamErr != nil {
		t.Errorf("stream yielded terminal error after downstream stop: %v", streamErr)
	}
}

// AssertPreCanceled requires one cancellation error and no normal observations.
// Use it with Scenario.PreCancel.
func AssertPreCanceled(t *testing.T, collected []llm.CompletionChunk, streamErr error) {
	t.Helper()
	if len(collected) != 0 {
		t.Errorf("pre-canceled stream yielded normal chunks = %#v, want none", collected)
	}
	AssertCancellationHonoured(t, collected, streamErr)
}

// AssertZeroTerminalChunk reports whether chunk is the required zero value for
// a terminal error. It is useful in custom conformance assertions.
func AssertZeroTerminalChunk(t *testing.T, chunk llm.CompletionChunk) {
	t.Helper()
	if !reflect.ValueOf(chunk).IsZero() {
		t.Errorf("terminal chunk = %#v, want zero CompletionChunk", chunk)
	}
}

// CollectContent joins every retained chunk's Content.
func CollectContent(chunks []llm.CompletionChunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString(chunk.Content)
	}
	return b.String()
}
