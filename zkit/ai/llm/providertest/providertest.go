// Package providertest exercises llm.Provider implementations against the
// provider-neutral streaming behavior consumers depend on.
//
// Each backend supplies wire-level scenario handlers because provider protocols
// differ. The harness constructs the real provider, verifies that Complete is
// lazy, and consumes its CompletionStream synchronously. Shared assertions then
// inspect owned copies of yielded chunks and the stream's terminal error.
package providertest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const defaultScenarioTimeout = 10 * time.Second

// Assertion validates the normal chunks retained from a stream invocation and
// its terminal error. A nil streamErr means the stream completed successfully by
// returning. The harness consumes terminal errors separately and never folds
// them into a CompletionChunk.
type Assertion func(t *testing.T, collected []llm.CompletionChunk, streamErr error)

// Scenario is one provider conformance check. Handler serves the backend's
// provider-specific wire response, Request is passed to Provider.Complete, and
// Assert validates the synchronously consumed stream.
type Scenario struct {
	// Name is the t.Run subtest name.
	Name string

	// Handler serves the provider-specific wire protocol for this scenario.
	Handler http.HandlerFunc

	// Request is passed to Provider.Complete. Run enables streaming unless the
	// request already does so.
	Request llm.CompletionRequest

	// Assert validates owned chunk copies and the terminal sequence error.
	Assert Assertion

	// CancelMidStream cancels the invocation context synchronously after the
	// first normal observation. If the provider has not yielded yet, Timeout is
	// still the cancellation and hard-stop deadline.
	CancelMidStream bool

	// PreCancel cancels the context before Complete constructs the stream. When
	// invoked, a conforming provider performs no I/O and yields one terminal
	// cancellation error paired with a zero chunk.
	PreCancel bool

	// StopAfter makes the harness stop after accepting this many normal chunks.
	// The resulting false yield must synchronously stop provider work without a
	// terminal error. Zero drains the stream normally.
	StopAfter int

	// Timeout bounds the scenario and supplies its context deadline. Zero uses
	// the package default.
	Timeout time.Duration
}

// Suite is the per-backend conformance harness. Factory builds a provider
// pointed at the scenario server URL.
type Suite struct {
	Factory   func(t *testing.T, baseURL string) llm.Provider
	Scenarios []Scenario
}

// Run executes every scenario as a parallel subtest. Complete is checked for
// request-I/O laziness before the returned stream is consumed directly in the
// same goroutine.
func Run(t *testing.T, s Suite) {
	t.Helper()
	for _, sc := range s.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			t.Parallel()
			runScenario(t, s.Factory, sc)
		})
	}
}

func runScenario(t *testing.T, factory func(t *testing.T, baseURL string) llm.Provider, sc Scenario) {
	t.Helper()

	var requests atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		sc.Handler(w, r)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	provider := factory(t, srv.URL)

	timeout := sc.Timeout
	if timeout <= 0 {
		timeout = defaultScenarioTimeout
	}
	deadlineCtx, stopDeadline := context.WithTimeout(t.Context(), timeout)
	defer stopDeadline()
	ctx, cancel := context.WithCancelCause(deadlineCtx)
	defer cancel(nil)
	if sc.PreCancel {
		cancel(context.Canceled)
	}

	req := sc.Request
	if !req.Stream {
		req.Stream = true
	}

	requestsBefore := requests.Load()
	stream := provider.Complete(ctx, req)
	if stream == nil {
		t.Fatal("Complete returned a nil CompletionStream")
	}
	if got := requests.Load(); got != requestsBefore {
		t.Fatalf("Complete performed request I/O before stream invocation: requests = %d, want %d", got, requestsBefore)
	}

	collected, streamErr := drive(t, stream, sc.CancelMidStream, sc.StopAfter, cancel)
	if sc.PreCancel {
		if got := requests.Load(); got != requestsBefore {
			t.Errorf("pre-canceled stream performed request I/O: requests = %d, want %d", got, requestsBefore)
		}
	}
	sc.Assert(t, collected, streamErr)
}

// drive invokes stream synchronously and retains owned clones of normal chunks.
// A terminal sequence error is kept separate. The harness also checks the
// canonical terminal shape: a non-nil error accompanies a zero chunk and is the
// final yielded observation.
func drive(
	t *testing.T,
	stream llm.CompletionStream,
	cancelMidStream bool,
	stopAfter int,
	cancel context.CancelCauseFunc,
) ([]llm.CompletionChunk, error) {
	t.Helper()

	var collected []llm.CompletionChunk
	var streamErr error
	terminalSeen := false
	for chunk, err := range stream {
		if terminalSeen {
			t.Errorf("stream yielded after terminal error: chunk = %#v, err = %v", chunk, err)
			continue
		}
		if err != nil {
			terminalSeen = true
			streamErr = err
			if !reflect.ValueOf(chunk).IsZero() {
				t.Errorf("terminal error chunk = %#v, want zero CompletionChunk", chunk)
			}
			continue
		}

		collected = append(collected, chunk.Clone())
		if cancelMidStream && len(collected) == 1 {
			cancel(context.Canceled)
		}
		if stopAfter > 0 && len(collected) >= stopAfter {
			break
		}
	}
	return collected, streamErr
}

// SimpleRequest builds a minimal streaming CompletionRequest with one user
// message.
func SimpleRequest(prompt string) llm.CompletionRequest {
	return llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		Stream:   true,
	}
}

// RequestWithTool builds a streaming request advertising an echo function for
// tool-call conformance scenarios.
func RequestWithTool(prompt string) llm.CompletionRequest {
	return llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		Tools: []llm.Tool{{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "echo",
				Description: "echo the text back",
				Parameters: llm.Schema{
					Type:       "object",
					Properties: map[string]llm.Schema{"text": {Type: "string"}},
					Required:   []string{"text"},
				},
			},
		}},
		Stream: true,
	}
}
