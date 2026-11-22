package litellm_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

func TestExecutionHeadersAndAffinityProtocols(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		for _, stream := range []bool{false, true} {
			for _, mode := range []llm.AffinityMode{llm.AffinityModes.AFFINITYNONE, llm.AffinityModes.AFFINITYPREFER} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", fixture.protocol, stream, mode), func(t *testing.T) {
					t.Parallel()
					var calls atomic.Int64
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.URL.Path != fixture.requestPath(stream) {
							t.Errorf("path = %q", r.URL.Path)
						}
						assertAuth(t, r, fixture.protocol)
						wantSession := ""
						if mode == llm.AffinityModes.AFFINITYPREFER {
							wantSession = "host-session"
						}
						if r.Header.Get("X-Litellm-Session-Id") != wantSession || r.Header.Get("X-Litellm-Trace-Id") != "" {
							t.Error("incorrect session or changing trace ID")
						}
						body, err := io.ReadAll(r.Body)
						if err != nil || !strings.Contains(string(body), "observation request") {
							t.Errorf("request body = %s, %v", body, err)
						}
						for _, forbidden := range []string{"host-session", "specific_deployment", "model_info"} {
							if strings.Contains(string(body), forbidden) {
								t.Errorf("affinity leaked into body: %s", forbidden)
							}
						}
						for key, values := range executionHeaders() {
							w.Header()[key] = values
						}
						fixture.serve(w, stream, true)
					}))
					t.Cleanup(server.Close)
					provider := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL), litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(server.Client()))
					req := providertest.SimpleRequest("observation request")
					req.Stream = stream
					req.Affinity = llm.Affinity{Mode: mode, SessionID: "host-session"}
					before := requestJSON(t, req)
					// The same host session survives separate protocol requests.
					for range 2 {
						observations := collectExecution(t, provider.Complete(t.Context(), req))
						if len(observations) != 1 {
							t.Fatalf("observations = %d, want one snapshot", len(observations))
						}
						want := llm.ExecutionObservation{
							RequestedModel: alias, GatewayDeployment: "deployment-opaque", ReportedModel: "reported-model",
							GatewayVersion: "gateway-version", GatewayCallID: "gateway-call", RetryCount: llm.ObservedCount{Count: 0, Known: true},
							FallbackCount: llm.ObservedCount{Count: 2, Known: true},
							ReportedCost:  llm.ReportedCost{Amount: 0.25, Reported: true, Source: "x-litellm-response-cost"},
						}
						if observations[0] != want {
							t.Errorf("observation = %+v, want %+v", observations[0], want)
						}
					}
					if calls.Load() != 2 || requestJSON(t, req) != before {
						t.Fatal("request mutated or extra protocol call")
					}
				})
			}
		}
	}
}

func executionHeaders() http.Header {
	return http.Header{
		"X-Litellm-Call-Id": {"gateway-call"}, "X-Litellm-Model-Id": {"deployment-opaque"},
		"X-Litellm-Model-Name": {"reported-model"}, "X-Litellm-Attempted-Retries": {"0"},
		"X-Litellm-Attempted-Fallbacks": {"2"}, "X-Litellm-Response-Cost": {"0.25"},
		// The runtime version is observed separately, never as a model version.
		"X-Litellm-Version": {"gateway-version"},
		// These are deliberately ignored, not evidence of route, whole-response
		// caching, server affinity support or cost currency.
		"X-Litellm-Cache-Key": {gatewaySecret},
		"X-Litellm-Api-Base":  {"https://example.invalid/" + gatewaySecret},
		"X-Litellm-Cache-Hit": {"true"}, "X-Litellm-Affinity-Support": {"true"},
		"X-Litellm-Cost-Currency": {"USD"}, "X-Arbitrary-Secret": {gatewaySecret},
	}
}

func collectExecution(t *testing.T, stream llm.CompletionStream) []llm.ExecutionObservation {
	t.Helper()
	var observations []llm.ExecutionObservation
	for chunk, err := range stream {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if chunk.Execution != nil {
			snapshot := chunk.Clone()
			// Returned chunk references are borrowed; retaining clones must own
			// the snapshot independently, including after consumer mutation.
			chunk.Execution.GatewayCallID = "consumer-mutation"
			observations = append(observations, *snapshot.Execution)
		}
	}
	return observations
}

func TestExecutionExternalScalars(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, raw string
		id        string
		count     llm.ObservedCount
		cost      llm.ReportedCost
	}{
		{name: "missing"},
		{name: "zero", raw: "0", id: "0", count: llm.ObservedCount{Known: true}, cost: llm.ReportedCost{Reported: true, Source: "x-litellm-response-cost"}},
		{name: "positive", raw: "12", id: "12", count: llm.ObservedCount{Count: 12, Known: true}, cost: llm.ReportedCost{Amount: 12, Reported: true, Source: "x-litellm-response-cost"}},
		{name: "decimal zero", raw: "0.0", id: "0.0", cost: llm.ReportedCost{Reported: true, Source: "x-litellm-response-cost"}},
		{name: "exponent zero", raw: "0e-9999", id: "0e-9999", cost: llm.ReportedCost{Reported: true, Source: "x-litellm-response-cost"}},
		{name: "negative zero", raw: "-0", id: "-0", cost: llm.ReportedCost{Reported: true, Source: "x-litellm-response-cost"}},
		{name: "negative", raw: "-1", id: "-1"},
		{name: "fraction", raw: "0.5", id: "0.5", cost: llm.ReportedCost{Amount: 0.5, Reported: true, Source: "x-litellm-response-cost"}},
		{name: "signed count", raw: "+1", id: "+1", cost: llm.ReportedCost{Amount: 1, Reported: true, Source: "x-litellm-response-cost"}},
		{name: "exponent count", raw: "1e2", id: "1e2", cost: llm.ReportedCost{Amount: 100, Reported: true, Source: "x-litellm-response-cost"}},
		{name: "count overflow", raw: "9223372036854775808", id: "9223372036854775808", cost: llm.ReportedCost{Amount: 9223372036854775808, Reported: true, Source: "x-litellm-response-cost"}},
		{name: "float overflow", raw: "1e999", id: "1e999"},
		{name: "float underflow", raw: "1e-9999", id: "1e-9999"},
		{name: "uppercase exponent underflow", raw: "1E-9999", id: "1E-9999"},
		{name: "negative underflow", raw: "-1e-9999", id: "-1e-9999"},
		{name: "subnormal", raw: "5e-324", id: "5e-324", cost: llm.ReportedCost{Amount: 5e-324, Reported: true, Source: "x-litellm-response-cost"}},
		{name: "hexadecimal", raw: "0x1p0", id: "0x1p0"},
		{name: "hexadecimal zero", raw: "0x0p0", id: "0x0p0"},
		{name: "digit separator", raw: "1_0", id: "1_0"},
		{name: "nan", raw: "NaN", id: "NaN"},
		{name: "infinity", raw: "+Inf", id: "+Inf"},
		{name: "unsigned infinity", raw: "Inf", id: "Inf"},
		{name: "negative infinity", raw: "-Inf", id: "-Inf"},
		{name: "malformed", raw: "unknown", id: "unknown"},
		{name: "count spaces", raw: " 1 ", id: " 1 "},
		{name: "long ID", raw: strings.Repeat("a", 257)},
		{name: "max ID", raw: strings.Repeat("a", 256), id: strings.Repeat("a", 256)},
		{name: "unicode ID", raw: "módèle", id: "módèle"},
		{name: "byte bound", raw: strings.Repeat("é", 129)},
		{name: "invalid UTF8", raw: string([]byte{0xff})},
		{name: "control", raw: "ab\x7fcd"},
		{name: "unicode control", raw: "ab\u0085cd"},
		{name: "newline", raw: "ab\ncd"},
		{name: "URL", raw: "https://example.invalid/private?key=secret"},
		{name: "key reflection", raw: "prefix-ordinary-test-key-suffix"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			headers := http.Header{}
			for _, key := range []string{"X-Litellm-Call-Id", "X-Litellm-Model-Id", "X-Litellm-Model-Name", "X-Litellm-Version", "X-Litellm-Attempted-Retries", "X-Litellm-Attempted-Fallbacks", "X-Litellm-Response-Cost"} {
				if test.raw != "" {
					headers.Set(key, test.raw)
				}
			}
			got := observedScalars(t, "ordinary-test-key", headers)
			if string(got.GatewayCallID) != test.id || string(got.GatewayDeployment) != test.id || string(got.ReportedModel) != test.id || string(got.GatewayVersion) != test.id || got.ReportedVersion != "" || got.RetryCount != test.count || got.FallbackCount != test.count || got.ReportedCost != test.cost {
				t.Errorf("observation = %+v", got)
			}
		})
	}
	t.Run("gateway runtime without model version", func(t *testing.T) {
		headers := http.Header{"X-Litellm-Version": {"gateway-runtime-only"}}
		got := observedScalars(t, "ordinary-test-key", headers)
		want := llm.ExecutionObservation{RequestedModel: alias, GatewayVersion: "gateway-runtime-only"}
		if got != want {
			t.Errorf("runtime-only observation = %+v, want %+v", got, want)
		}
	})
	t.Run("duplicate values", func(t *testing.T) {
		headers := executionHeaders()
		for key, values := range headers {
			headers[key] = append(values, "another")
		}
		got := observedScalars(t, "ordinary-test-key", headers)
		if got != (llm.ExecutionObservation{RequestedModel: alias}) {
			t.Errorf("ambiguous observation = %+v", got)
		}
	})
	t.Run("numeric key reflection", func(t *testing.T) {
		headers := http.Header{"X-Litellm-Response-Cost": {"123"}, "X-Litellm-Attempted-Retries": {"123"}}
		got := observedScalars(t, "123", headers)
		if got.ReportedCost.Reported || got.RetryCount.Known {
			t.Errorf("reflected numeric key = %+v", got)
		}
	})
}

func observedScalars(t *testing.T, apiKey string, headers http.Header) llm.ExecutionObservation {
	t.Helper()
	fixture := fixtures()[0]
	provider := litellm.NewProvider(apiKey, litellm.WithModel(alias), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		fixture.serve(recorder, false, true)
		response := recorder.Result()
		for key, values := range headers {
			response.Header[key] = values
		}
		response.Request = r
		return response, nil
	})}))
	req := providertest.SimpleRequest("hi")
	req.Stream = false
	observations := collectExecution(t, provider.Complete(t.Context(), req))
	if len(observations) != 1 {
		t.Fatalf("observations = %d", len(observations))
	}
	return observations[0]
}

func TestRequireAffinityLazyBeforeSetup(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			// Invalid base URL and empty Google key would fail codec setup if
			// attempted. The public affinity error must win over either failure.
			provider := litellm.NewProvider("", litellm.WithBaseURL(":invalid"), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.New("unexpected HTTP")
			})}))
			req := providertest.SimpleRequest("hi")
			req.Affinity = llm.Affinity{Mode: llm.AffinityModes.AFFINITYREQUIRE, SessionID: "host-session"}
			stream := provider.Complete(t.Context(), req)
			if stream == nil || calls.Load() != 0 {
				t.Fatal("eager Complete")
			}
			var yields int
			for chunk, err := range stream {
				yields++
				if !errors.Is(err, litellm.ErrAffinityUnsupported) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
					t.Fatalf("require = %+v, %v", chunk, err)
				}
			}
			if yields != 1 || calls.Load() != 0 {
				t.Fatalf("yields = %d, HTTP calls = %d", yields, calls.Load())
			}
		})
	}
}

func TestExecutionConcurrentSessions(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				session := r.Header.Get("X-Litellm-Session-Id")
				body, err := io.ReadAll(r.Body)
				if err != nil || !strings.Contains(string(body), session) || session == "" {
					t.Error("cross-session request")
				}
				w.Header().Set("X-Litellm-Call-Id", session)
				fixture.serve(w, false, true)
			}))
			t.Cleanup(server.Close)
			provider := litellm.NewProvider("ordinary-test-key", litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithBaseURL(server.URL))
			for i := range 8 {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					t.Parallel()
					session := fmt.Sprintf("host-session-%d", i)
					req := providertest.SimpleRequest(session)
					req.Stream = false
					req.Affinity = llm.Affinity{Mode: llm.AffinityModes.AFFINITYPREFER, SessionID: llm.SessionID(session)}
					for range 2 {
						observations := collectExecution(t, provider.Complete(t.Context(), req))
						if len(observations) != 1 || string(observations[0].GatewayCallID) != session {
							t.Fatalf("cross-session observation = %+v", observations)
						}
					}
				})
			}
		})
	}
}

func TestExecutionEarlyStopAndErrors(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			fixture.serve(recorder, true, true)
			body := &trackedBody{Reader: strings.NewReader(recorder.Body.String())}
			provider := litellm.NewProvider("ordinary-test-key", litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				headers := executionHeaders()
				headers.Set("Content-Type", "text/event-stream")
				return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: body, Request: r}, nil
			})}))
			var yields int
			provider.Complete(t.Context(), providertest.SimpleRequest("hi"))(func(chunk llm.CompletionChunk, err error) bool {
				yields++
				if err != nil || chunk.Execution == nil {
					t.Errorf("first chunk = %+v, %v", chunk, err)
				}
				return false
			})
			if yields != 1 || !body.closed.Load() {
				t.Fatal("extra yields or unclosed body after false")
			}
			for _, status := range []int{http.StatusUnauthorized, http.StatusOK} {
				t.Run(strconv.Itoa(status), func(t *testing.T) {
					errorBody := executionErrorBody(fixture.protocol)
					closed := &trackedBody{Reader: strings.NewReader(errorBody)}
					provider := litellm.NewProvider("ordinary-test-key", litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
						headers := executionHeaders()
						headers.Set("Content-Type", "text/event-stream")
						return &http.Response{StatusCode: status, Header: headers, Body: closed, Request: r}, nil
					})}))
					var errorsSeen int
					for chunk, err := range provider.Complete(t.Context(), providertest.SimpleRequest("hi")) {
						errorsSeen++
						if errorsSeen != 1 || !errors.Is(err, llm.ErrInvalidAPIKey) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
							t.Fatalf("terminal = %+v, %v", chunk, err)
						}
						assertSafeError(t, err)
					}
					if errorsSeen != 1 || !closed.closed.Load() {
						t.Fatal("missing error or unclosed body")
					}
				})
			}
		})
	}
}

func executionErrorBody(protocol llm.ModelProtocol) string {
	switch protocol {
	case llm.ModelProtocols.PROTOCOLRESPONSES:
		return "data: " + `{"type":"error","code":"invalid_api_key","message":"` + gatewaySecret + `"}` + "\n\n"
	case llm.ModelProtocols.PROTOCOLMESSAGES:
		return "event: error\ndata: " + `{"type":"error","error":{"type":"authentication_error","message":"` + gatewaySecret + `"}}` + "\n\n"
	case llm.ModelProtocols.PROTOCOLGENERATECONTENT:
		return "data: " + `{"error":{"code":401,"status":"UNAUTHENTICATED","message":"` + gatewaySecret + `"}}` + "\n\n"
	default:
		return "data: " + `{"error":{"code":"invalid_api_key","message":"` + gatewaySecret + `"}}` + "\n\n"
	}
}

func TestExecutionSanitizedRequestedModel(t *testing.T) {
	t.Parallel()
	fixture := fixtures()[0]
	provider := litellm.NewProvider("ordinary-test-key", litellm.WithModel("alias-ordinary-test-key"), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		fixture.serve(recorder, false, true)
		response := recorder.Result()
		response.Request = r
		return response, nil
	})}))
	req := providertest.SimpleRequest("hi")
	req.Stream = false
	for _, observation := range collectExecution(t, provider.Complete(t.Context(), req)) {
		if observation != (llm.ExecutionObservation{}) {
			t.Fatalf("secret-bearing request alias was not omitted: %+v", observation)
		}
	}
}

func TestExecutionEmptySuccessfulStream(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			body := &trackedBody{Reader: strings.NewReader("")}
			provider := litellm.NewProvider("ordinary-test-key", litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				headers := executionHeaders()
				headers.Set("Content-Type", "text/event-stream")
				return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: body, Request: r}, nil
			})}))
			var yields int
			for chunk, err := range provider.Complete(t.Context(), providertest.SimpleRequest("hi")) {
				yields++
				// The Messages codec reports final usage even for empty EOF.
				// Preserve that ordinary chunk rather than changing native codecs.
				if fixture.protocol != llm.ModelProtocols.PROTOCOLMESSAGES || yields != 1 || err != nil || !chunk.UsageReported || chunk.Execution == nil {
					t.Fatalf("header produced a synthetic yield: %+v, %v", chunk, err)
				}
			}
			if !body.closed.Load() {
				t.Fatal("empty response body left open")
			}
		})
	}
}

func TestExecutionPartialStreamTerminalFailure(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			prefix := ""
			switch fixture.protocol {
			case llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS:
				prefix = "data: " + `{"id":"partial","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"
			case llm.ModelProtocols.PROTOCOLRESPONSES:
				prefix = "data: " + `{"type":"response.output_text.delta","delta":"partial"}` + "\n\n"
			case llm.ModelProtocols.PROTOCOLMESSAGES:
				prefix = "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"partial","type":"message","role":"assistant","model":"ignored","content":[],"usage":{"input_tokens":0,"output_tokens":0}}}` + "\n\n" +
					"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
					"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}` + "\n\n"
			case llm.ModelProtocols.PROTOCOLGENERATECONTENT:
				prefix = "data: " + `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}` + "\n\n"
			}
			body := &trackedBody{Reader: strings.NewReader(prefix + executionErrorBody(fixture.protocol))}
			provider := litellm.NewProvider("ordinary-test-key", litellm.WithModel(alias), litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				headers := executionHeaders()
				headers.Set("Content-Type", "text/event-stream")
				return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: body, Request: r}, nil
			})}))
			var observations, terminal int
			var content strings.Builder
			for chunk, err := range provider.Complete(t.Context(), providertest.SimpleRequest("hi")) {
				if terminal != 0 {
					t.Fatal("yield after terminal failure")
				}
				if err != nil {
					terminal++
					if !errors.Is(err, llm.ErrInvalidAPIKey) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
						t.Fatalf("terminal = %+v, %v", chunk, err)
					}
					continue
				}
				content.WriteString(chunk.Content)
				if chunk.Execution != nil {
					observations++
				}
			}
			if observations != 1 || terminal != 1 || content.String() != "partial" || !body.closed.Load() {
				t.Fatalf("observations=%d terminal=%d content=%q closed=%t", observations, terminal, content.String(), body.closed.Load())
			}
		})
	}
}
