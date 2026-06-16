// Package providertest exercises every llm.Provider implementation
// against the common behaviours the runner depends on. The wire
// shape per backend is too divergent for a single stub server to
// speak (OpenAI's chat completions vs. Anthropic's messages vs.
// Codex's responses API), so each backend supplies a per-scenario
// http.HandlerFunc + a Factory that wires a Provider against the
// scenario's test server URL. The assertions on top of the resulting
// chunks are reusable.
//
// # How to wire a backend
//
// Each backend ships a file like `conformance_test.go` that
// constructs a Suite and calls Run:
//
//	func TestProvider_Conformance(t *testing.T) {
//	    providertest.Run(t, providertest.Suite{
//	        Factory: func(t *testing.T, baseURL string) llm.Provider {
//	            p, _ := openai.NewProvider("test", openai.WithBaseURL(baseURL))
//	            return p
//	        },
//	        Scenarios: []providertest.Scenario{
//	            {
//	                Name:    "Cancellation",
//	                Handler: provHangForever(),
//	                Request: providertest.SimpleRequest("ignored"),
//	                Assert:  providertest.AssertCancellationHonoured,
//	            },
//	            // ... more scenarios
//	        },
//	    })
//	}
//
// The library half (Scenario, Suite.Run, the Assert* helpers) is
// shared across backends. The bytes returned by each Handler are the
// piece that has to be hand-rolled per provider because every
// provider invented its own response shape.
package providertest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// defaultScenarioTimeout caps how long a single Suite scenario will
// wait for its provider to drain. Generous enough that a sluggish
// CI doesn't false-positive on the happy path, tight enough that a
// failing-to-cancel provider doesn't wedge the whole test run.
const defaultScenarioTimeout = 10 * time.Second

// Scenario is one conformance check. The Handler is the wire-level
// stub for the provider's HTTP backend; the Factory call (on the
// owning Suite) wires a real Provider against the stub URL; the
// suite drives Complete with Request; Assert validates the chunks
// the Provider yielded.
type Scenario struct {
	// Name is the t.Run subtest name. Suite.Run prefixes nothing —
	// the per-backend test owns the framing.
	Name string

	// Handler is the http.HandlerFunc the test server serves. The
	// shape of what it returns is provider-specific (SSE for the
	// streaming providers, plain JSON for non-streaming, whatever
	// the provider's wire protocol expects).
	Handler http.HandlerFunc

	// Request is the input handed to Provider.Complete. The Suite
	// fills in Stream=true unless overridden — most assertions
	// expect streaming.
	Request llm.CompletionRequest

	// Assert is run after Complete returns and the chunks channel
	// has drained (or the per-scenario timeout fires). collected
	// is the ordered list of every CompletionChunk the provider
	// produced; completeErr is the error returned by Complete
	// itself (separate from any chunk.Error).
	Assert func(t *testing.T, collected []llm.CompletionChunk, completeErr error)

	// CancelMidStream, when true, instructs the suite to cancel
	// the per-scenario ctx after the first chunk arrives (or after
	// 100ms, whichever happens first). Use this with Handler stubs
	// that hang forever to exercise cancellation cleanly.
	CancelMidStream bool

	// Timeout overrides defaultScenarioTimeout for slow scenarios.
	// Zero means default.
	Timeout time.Duration
}

// Suite is the per-backend conformance harness. Factory builds a
// Provider pointed at the per-scenario test server URL.
type Suite struct {
	// Factory builds a Provider pointed at baseURL. The Provider
	// must be ready to call Complete immediately — any handshake
	// (OpenAICodex's token refresh, etc.) happens inside Factory.
	Factory func(t *testing.T, baseURL string) llm.Provider

	// Scenarios is the list of conformance checks to run. Empty
	// scenarios is allowed (the Suite is a no-op), useful while
	// staging adoption.
	Scenarios []Scenario
}

// Run executes each scenario as a t.Run subtest. Per scenario:
//
//  1. Spin up an httptest.Server with the scenario's Handler.
//  2. Call Factory to build a Provider against the server URL.
//  3. Call Provider.Complete with the scenario's Request.
//  4. Drain the chunks channel (or cancel mid-stream).
//  5. Hand the collected chunks + completeErr to Assert.
//  6. Tear the server down.
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
	srv := httptest.NewServer(sc.Handler)
	defer srv.Close()

	p := factory(t, srv.URL)
	if p == nil {
		t.Fatalf("Factory returned nil provider")
	}

	timeout := sc.Timeout
	if timeout <= 0 {
		timeout = defaultScenarioTimeout
	}
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	req := sc.Request
	if !req.Stream {
		req.Stream = true
	}

	collected, completeErr := drive(ctx, p, req, sc.CancelMidStream, cancel)
	sc.Assert(t, collected, completeErr)
}

// drive runs the provider's Complete stream and collects chunks into the
// (collected, completeErr) shape the asserts expect. A yield error is folded
// into chunk.Error so the channel-era assertions apply unchanged.
func drive(ctx context.Context, p llm.Provider, req llm.CompletionRequest, cancelMid bool, cancel context.CancelFunc) ([]llm.CompletionChunk, error) {
	seq, err := p.Complete(ctx, req)
	if err != nil || seq == nil {
		return nil, err
	}
	c := newCollector(cancelMid, cancel)
	for chunk, yerr := range seq {
		if yerr != nil {
			if chunk.Error == nil {
				chunk.Error = yerr
			}
			c.add(chunk)
			break
		}
		c.add(chunk)
	}
	return c.collected, nil
}

// collector accumulates chunks and, when cancelMid is set, cancels the ctx
// after the first chunk arrives or 100ms — whichever comes first (the timer
// guards a handler that hangs and yields nothing).
type collector struct {
	collected []llm.CompletionChunk
	cancelMid bool
	armed     chan struct{}
	first     bool
}

func newCollector(cancelMid bool, cancel context.CancelFunc) *collector {
	c := &collector{cancelMid: cancelMid, first: true}
	if cancelMid {
		c.armed = make(chan struct{})
		go func() {
			select {
			case <-c.armed:
				cancel()
			case <-time.After(100 * time.Millisecond):
				cancel()
			}
		}()
	}
	return c
}

func (c *collector) add(chunk llm.CompletionChunk) {
	c.collected = append(c.collected, chunk)
	if c.cancelMid && c.first {
		c.first = false
		close(c.armed)
	}
}

// SimpleRequest builds a minimal CompletionRequest for use in
// scenarios that don't care about the input — most cancellation /
// usage / streaming tests. Streaming is forced on; the user message
// is the single supplied prompt.
func SimpleRequest(prompt string) llm.CompletionRequest {
	return llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		Stream: true,
	}
}

// RequestWithTool returns a request that advertises one tool the
// provider should be able to surface as a tool_call. Used by the
// tool-call conformance scenario.
func RequestWithTool(prompt string) llm.CompletionRequest {
	return llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		Tools: []llm.Tool{
			{
				Type: "function",
				Function: llm.ToolFunction{
					Name:        "echo",
					Description: "echo the text back",
					Parameters: llm.Schema{
						Type: "object",
						Properties: map[string]llm.Schema{
							"text": {Type: "string"},
						},
						Required: []string{"text"},
					},
				},
			},
		},
		Stream: true,
	}
}
