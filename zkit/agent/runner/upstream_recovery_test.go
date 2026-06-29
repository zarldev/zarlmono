package runner_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/agent/taskscope"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Recovery contract: when the upstream LLM server returns the
// signature "Failed to parse tool call arguments as JSON" inside a
// stream error, the runner injects a corrective user message and
// re-iterates instead of bailing out as terminal. This covers the
// llama-server --jinja path's strict tool-call JSON validation
// (we hit it on gohugoio__hugo-12448 during the swebench eval —
// model emitted unescaped quotes inside a multi-line code edit
// payload and llama-server returned 500 before the chunks reached
// our downstream repair.Unmarshal).

// chunkErr builds a stream chunk carrying the given error. The
// runner's drain loop translates this into streamErr and exits the
// iteration's read phase.
func chunkErr(err error) llm.CompletionChunk {
	return llm.CompletionChunk{Error: err}
}

func TestRun_RecoversFromUpstreamToolCallJSONError(t *testing.T) {
	t.Parallel()
	// Turn 0 fails with the signature error. Turn 1 is a clean
	// text-only completion (the model's retry after the corrective
	// nudge). The Run loop must reach turn 1 — proving the recovery
	// fired — and finish without a terminal error.
	prov := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkErr(errors.New(
				"streaming error: 500 Internal Server Error " +
					`{"code":500,"message":"Failed to parse tool call arguments as JSON: bad escape"}`))},
			{chunkText("recovered"), chunkDone()},
		},
	}
	reg := newRegistry()
	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg), runner.WithMaxIterations(5))

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "anything",
	})
	if res.Err != nil {
		t.Fatalf("Run returned res.Err: %v", res.Err)
	}
	if res.Reason == runner.TerminalError {
		t.Errorf("expected non-terminal completion, got TerminalError with err=%v", res.Err)
	}
	if prov.callCount() < 2 {
		t.Errorf("expected runner to retry after upstream JSON rejection; provider saw %d calls", prov.callCount())
	}
	if !strings.Contains(res.FinalContent, "recovered") {
		t.Errorf("expected final content from the retry turn, got %q", res.FinalContent)
	}
	// The corrective user message must have been injected so the
	// model sees what to do differently. Check the final history.
	var sawNudge bool
	for _, m := range res.Messages {
		if m.Role == "user" && strings.Contains(m.Content, "tool-call arguments JSON was malformed") {
			sawNudge = true
			break
		}
	}
	if !sawNudge {
		t.Errorf("expected corrective user message in result history; got %d messages: %+v",
			len(res.Messages), res.Messages)
	}
}

// Cap: after toolCallJSONRecoverLimit (3) consecutive failures with
// the same upstream signature, we give up and surface a terminal
// error. Without the cap a model stuck producing bad JSON would loop
// forever inside one Run call.
func TestRun_StopsRecoveringAfterCap(t *testing.T) {
	t.Parallel()
	badChunk := chunkErr(errors.New(
		"500: Failed to parse tool call arguments as JSON: still bad"))
	// 4 consecutive failures: 3 recoveries fire, the 4th surfaces
	// terminal.
	prov := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{badChunk}, {badChunk}, {badChunk}, {badChunk},
		},
	}
	reg := newRegistry()
	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg), runner.WithMaxIterations(10))

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "anything",
	})
	if res.Reason != runner.TerminalError {
		t.Errorf(
			"expected TerminalError after %d consecutive bad-JSON failures; got reason=%v",
			prov.callCount(),
			res.Reason,
		)
	}
	if got, want := prov.callCount(), 4; got != want {
		t.Errorf("expected provider to be called exactly %d times (3 recoveries + 1 terminal); got %d", want, got)
	}
}

// Unrelated stream errors must still terminate immediately — the
// recovery path is narrow on purpose. A generic 500 or a 503 isn't
// recovered, only the specific "Failed to parse tool call arguments
// as JSON" signature.
func TestRun_DoesNotRecoverFromUnrelatedStreamError(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkErr(errors.New("503 Service Unavailable: backend overloaded"))},
		},
	}
	reg := newRegistry()
	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg), runner.WithMaxIterations(5))

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "anything",
	})
	if res.Reason != runner.TerminalError {
		t.Errorf("503 should terminate, not recover; got reason=%v", res.Reason)
	}
	if prov.callCount() != 1 {
		t.Errorf("503 should NOT trigger a retry; provider saw %d calls (want 1)", prov.callCount())
	}
}

// Empty-stream recovery: a provider that opens the stream and then
// closes it with an EOF-class decode error ("unexpected end of JSON
// input") having emitted no content and no tool calls is the DeepSeek
// gateway-cut scenario. It's tagged ErrEmptyStream and retried — the
// retry turn (a clean completion, as if the provider's prefill cache
// warmed) must be reached and the Run must finish non-terminal. No
// corrective user message is injected: the request is re-issued as-is.
func TestRun_RetriesEmptyStream(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkErr(errors.New("stream: unexpected end of JSON input"))},
			{chunkText("recovered after empty stream"), chunkDone()},
		},
	}
	reg := newRegistry()
	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg),
		runner.WithMaxIterations(5),
		runner.WithEmptyStreamBackoff(0)) // immediate retry — keep the test fast

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "anything",
	})
	if res.Err != nil {
		t.Fatalf("Run returned res.Err: %v", res.Err)
	}
	if res.Reason == runner.TerminalError {
		t.Errorf("expected non-terminal completion after empty-stream retry, got TerminalError err=%v", res.Err)
	}
	if prov.callCount() < 2 {
		t.Errorf("expected runner to retry the empty stream; provider saw %d calls", prov.callCount())
	}
	if !strings.Contains(res.FinalContent, "recovered after empty stream") {
		t.Errorf("expected final content from the retry turn, got %q", res.FinalContent)
	}
	// The retry must NOT inject a corrective message — the request is
	// re-issued verbatim. Only the original prompt should be present.
	for _, m := range res.Messages {
		if m.Role == "user" && strings.Contains(m.Content, "malformed") {
			t.Errorf("empty-stream retry must not inject a corrective message; got %q", m.Content)
		}
	}
}

// Cap: after emptyStreamRetryLimit (3) consecutive empty streams, the
// 4th surfaces as terminal — tagged ErrEmptyStream — instead of
// retrying forever against a backend that's genuinely down.
func TestRun_StopsRetryingEmptyStreamAfterCap(t *testing.T) {
	t.Parallel()
	empty := chunkErr(errors.New("stream: unexpected end of JSON input"))
	prov := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{empty}, {empty}, {empty}, {empty},
		},
	}
	reg := newRegistry()
	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg),
		runner.WithMaxIterations(10),
		runner.WithEmptyStreamBackoff(0))

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "anything",
	})
	if res.Reason != runner.TerminalError {
		t.Errorf("expected TerminalError after the retry cap; got reason=%v", res.Reason)
	}
	if !errors.Is(res.Err, runner.ErrEmptyStream) {
		t.Errorf("terminal error after cap should still wrap ErrEmptyStream; got %v", res.Err)
	}
	if got, want := prov.callCount(), 4; got != want {
		t.Errorf("expected exactly %d calls (3 retries + 1 terminal); got %d", want, got)
	}
}

// Guard: the same EOF-class decode error after the stream has already
// emitted content is NOT the empty-stream scenario — it's a genuine
// mid-output truncation — and must NOT be tagged with ErrEmptyStream,
// so a future retry path can't silently re-run a half-completed turn.
func TestRun_DoesNotTagDecodeErrorAfterContent(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{
		turns: [][]llm.CompletionChunk{
			{chunkText("partial answer"), chunkErr(errors.New("unexpected end of JSON input"))},
		},
	}
	reg := newRegistry()
	r := runner.New(runner.ClientFromProvider(prov), runner.WithTools(reg), runner.WithMaxIterations(5))

	res := r.Run(t.Context(), runner.TaskSpec{
		ID:     taskscope.ID(uuid.NewString()),
		Prompt: "anything",
	})
	if errors.Is(res.Err, runner.ErrEmptyStream) {
		t.Errorf("decode error after real content must not be tagged ErrEmptyStream; got %v", res.Err)
	}
}
