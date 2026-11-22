package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func fragmentedInteractions() string {
	frames := []string{
		`{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"first","name":"echo","arguments":{}}}`,
		`{"event_type":"step.start","index":1,"step":{"type":"function_call","id":"second","name":"echo","arguments":{}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"arguments_delta","arguments":"{\"text\":"}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"arguments_delta","arguments":"{\"text\":\"second\"}"}}`,
		`{"event_type":"step.stop","index":1}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"arguments_delta","arguments":"\"first\"}"}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"interaction.completed","interaction":{"id":"ignored","status":"requires_action"}}`,
	}
	var out strings.Builder
	for _, frame := range frames {
		out.WriteString(interactionFrame(frame))
	}
	out.WriteString("data: [DONE]\n\n")
	return out.String()
}

func TestInteractionsFragmentedParallelCalls(t *testing.T) {
	t.Parallel()
	body := &interactionBody{frames: []string{fragmentedInteractions()}}
	provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		response := interactionResponse(r, "text/event-stream", "")
		response.Body = body
		return response, nil
	}})
	chunks, err := collectInteractions(t.Context(), provider, interactionRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	if !body.closed {
		t.Fatal("stream not closed")
	}
	var calls []llm.ToolCall
	for index, chunk := range chunks {
		if len(chunk.ToolCalls) > 0 && index != len(chunks)-1 {
			t.Fatal("executable calls published before terminal")
		}
		calls = append(calls, chunk.ToolCalls...)
	}
	if len(calls) != 2 || calls[0].ID != "first" || calls[1].ID != "second" || calls[0].Function.Arguments != `{"text":"first"}` || *calls[1].OutputIndex != 1 {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestInteractionsUsagePresenceAndCumulative(t *testing.T) {
	t.Parallel()
	for _, streaming := range []bool{false, true} {
		for _, test := range []struct {
			name, usage string
			want        llm.Usage
			reported    bool
		}{
			{name: "absent"},
			{name: "zero", usage: `{"total_thought_tokens":0}`, reported: true, want: llm.Usage{ReasoningTokensReported: true}},
			{name: "output-subtotal", usage: `{"total_input_tokens":5,"total_output_tokens":3,"total_thought_tokens":7,"total_cached_tokens":2,"total_tokens":15}`, reported: true, want: llm.Usage{PromptTokens: 5, CompletionTokens: 10, ReasoningTokens: 7, ReasoningTokensReported: true, CachedTokens: 2, TotalTokens: 15}},
			{name: "thought-absent", usage: `{"total_output_tokens":0,"total_tokens":0}`, reported: true},
		} {
			t.Run(fmt.Sprintf("%t/%s", streaming, test.name), func(t *testing.T) {
				t.Parallel()
				provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
					if streaming {
						out := interactionSSE([]string{`{"type":"model_output","content":[{"type":"text","text":"answer"}]}`}, "completed", test.usage)
						if test.reported {
							out = strings.Replace(out, interactionFrame(`{"event_type":"step.stop","index":0}`), interactionFrame(`{"event_type":"step.stop","index":0,"usage":{"total_input_tokens":100,"total_output_tokens":100},"step_usage":{"total_input_tokens":999}}`), 1)
						}
						return interactionResponse(r, "text/event-stream", out), nil
					}
					out := interactionAnswer
					if test.usage != "" {
						out = strings.TrimSuffix(out, "}") + `,"usage":` + test.usage + `}`
					}
					return interactionResponse(r, "application/json", out), nil
				}})
				chunks, err := collectInteractions(t.Context(), provider, interactionRequest(streaming))
				if err != nil {
					t.Fatal(err)
				}
				final := chunks[len(chunks)-1]
				if final.UsageReported != test.reported || final.Usage != test.want {
					t.Fatalf("usage=%+v reported=%t", final.Usage, final.UsageReported)
				}
			})
		}
	}
}

func TestInteractionsTerminalFailuresNeverExposeExecutableCalls(t *testing.T) {
	t.Parallel()
	valid := fragmentedInteractions()
	tests := []struct {
		name, body string
		want       error
	}{
		{"missing-done", strings.TrimSuffix(valid, "data: [DONE]\n\n"), google.ErrInteractionsProtocol},
		{"premature", interactionFrame(`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`), google.ErrInteractionsProtocol},
		{"incomplete-step", strings.Replace(valid, interactionFrame(`{"event_type":"step.stop","index":0}`), "", 1), google.ErrInteractionsProtocol},
		{"invalid-args", strings.Replace(valid, `\"first\"}`, `\"first\"`, 1), google.ErrInteractionsProtocol},
		{"empty-args-fragment", interactionFrame(`{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"empty","name":"echo","arguments":{}}}`) + interactionFrame(`{"event_type":"step.delta","index":0,"delta":{"type":"arguments_delta","arguments":""}}`) + interactionFrame(`{"event_type":"step.stop","index":0}`) + interactionFrame(`{"event_type":"interaction.completed","interaction":{"id":"ignored","status":"requires_action"}}`) + "data: [DONE]\n\n", google.ErrInteractionsProtocol},
		{"duplicate-id", strings.ReplaceAll(valid, `"id":"second"`, `"id":"first"`), google.ErrInteractionsProtocol},
		{"completed-with-calls", strings.ReplaceAll(valid, "requires_action", "completed"), google.ErrInteractionsProtocol},
		{"failed", strings.ReplaceAll(valid, "requires_action", "failed"), google.ErrInteractionsProtocol},
		{"cancelled", strings.ReplaceAll(valid, "requires_action", "cancelled"), google.ErrInteractionsProtocol},
		{"unknown-status", strings.ReplaceAll(valid, "requires_action", "secret-status"), google.ErrInteractionsProtocol},
		{"in-band-error", strings.Replace(valid, `"event_type":"interaction.completed"`, `"event_type":"error","error":{"code":"secret-code","message":"secret-message"},"unused":true`, 1), google.ErrInteractionsProtocol},
		{"unsupported-step", interactionSSE([]string{`{"type":"google_search_call"}`}, "completed", ""), llm.ErrModelNotSupported},
		{"unknown-step", interactionSSE([]string{`{"type":"secret-family"}`}, "completed", ""), llm.ErrModelNotSupported},
		{"media", interactionSSE([]string{`{"type":"model_output","content":[{"type":"image","data":"secret-image","mime_type":"image/png"}]}`}, "completed", ""), llm.ErrModelNotSupported},
		{"delta-without-start", interactionFrame(`{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"secret-text"}}`) + "data: [DONE]\n\n", google.ErrInteractionsProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := &interactionBody{frames: []string{test.body}}
			provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				response := interactionResponse(r, "text/event-stream", "")
				response.Body = body
				return response, nil
			}})
			errorsSeen := 0
			for chunk, err := range provider.Complete(t.Context(), interactionRequest(true)) {
				if len(chunk.ToolCalls) != 0 {
					t.Fatal("executable call escaped failed stream")
				}
				if err != nil {
					errorsSeen++
					if !errors.Is(err, test.want) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
						t.Fatalf("terminal chunk=%+v err=%v", chunk, err)
					}
					if strings.Contains(err.Error(), "secret") {
						t.Fatalf("unsafe diagnostic=%v", err)
					}
				} else if errorsSeen > 0 {
					t.Fatal("yield after terminal error")
				}
			}
			if errorsSeen != 1 || !body.closed {
				t.Fatalf("errors=%d closed=%t", errorsSeen, body.closed)
			}
		})
	}
}

type interactionBody struct {
	frames []string
	closed bool
	reads  int
	onRead func() error
}

func (b *interactionBody) Read(p []byte) (int, error) {
	b.reads++
	if b.onRead != nil {
		return 0, b.onRead()
	}
	if len(b.frames) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.frames[0])
	b.frames[0] = b.frames[0][n:]
	if b.frames[0] == "" {
		b.frames = b.frames[1:]
	}
	return n, nil
}

func (b *interactionBody) Close() error { b.closed = true; return nil }

func TestInteractionsEarlyStopClosesImmediately(t *testing.T) {
	t.Parallel()
	prefix := interactionFrame(`{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[{"type":"text","text":"answer"}]}}`) + interactionFrame(`{"event_type":"step.stop","index":0}`)
	body := &interactionBody{frames: []string{prefix, "must never be read"}}
	provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		response := interactionResponse(r, "text/event-stream", "")
		response.Body = body
		return response, nil
	}})
	cause := errors.New("stop also cancels")
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	count := 0
	provider.Complete(ctx, interactionRequest(true))(func(chunk llm.CompletionChunk, err error) bool {
		count++
		if err != nil || chunk.Content != "answer" {
			t.Errorf("chunk=%+v err=%v", chunk, err)
		}
		cancel(cause)
		return false
	})
	if count != 1 || !body.closed || body.reads != 1 {
		t.Fatalf("count=%d closed=%t reads=%d", count, body.closed, body.reads)
	}
}

func TestInteractionsCancellationCauseHeadersAndReads(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"headers", "reads"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("owned cancellation")
			ctx, cancel := context.WithCancelCause(t.Context())
			defer cancel(nil)
			body := &interactionBody{onRead: func() error { cancel(cause); <-ctx.Done(); return errors.New("secret-read") }}
			provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				if stage == "headers" {
					cancel(cause)
					<-r.Context().Done()
					return nil, errors.New("secret-headers")
				}
				response := interactionResponse(r, "text/event-stream", "")
				response.Body = body
				return response, nil
			}})
			_, err := collectInteractions(ctx, provider, interactionRequest(true))
			if !errors.Is(err, cause) {
				t.Fatalf("cause=%v", err)
			}
			if stage == "reads" && !body.closed {
				t.Fatal("cancelled body not closed")
			}
		})
	}
}

func TestInteractionsSafeHTTPErrorsAndNoRetries(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			calls := 0
			body := &interactionBody{frames: []string{`{"error":{"message":"secret-body","code":500}}`}}
			provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				calls++
				response := interactionResponse(r, "application/json", "")
				response.StatusCode = status
				response.Body = body
				return response, nil
			}})
			_, err := collectInteractions(t.Context(), provider, interactionRequest(false))
			if !errors.Is(err, llm.ErrProviderUnavailable) || calls != 1 || !body.closed || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err=%v requests=%d closed=%t", err, calls, body.closed)
			}
		})
	}
}

func TestInteractionsThoughtAndAnnotatedTextDeltas(t *testing.T) {
	t.Parallel()
	frames := []string{
		`{"event_type":"step.start","index":0,"step":{"type":"thought"}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"reason "}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_summary","content":{"type":"text","text":"summary"}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"thought_signature","signature":"signed"}}`,
		`{"event_type":"step.stop","index":0}`,
		`{"event_type":"step.start","index":1,"step":{"type":"model_output"}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"text","text":"an"},"metadata":{"total_usage":{"total_input_tokens":100}}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"text","text":"swer"}}`,
		`{"event_type":"step.delta","index":1,"delta":{"type":"text_annotation_delta","annotations":[{"type":"url_citation","start_index":0,"end_index":6,"url":"https://example.invalid/source","title":"source"}]}}`,
		`{"event_type":"step.stop","index":1}`,
		`{"event_type":"interaction.status_update","interaction_id":"ignored","status":"completed","usage":{"total_input_tokens":2}}`,
		`{"event_type":"interaction.completed","interaction":{"id":"ignored","status":"completed","usage":{"total_input_tokens":3,"total_thought_tokens":0}}}`,
	}
	var sse strings.Builder
	for _, frame := range frames {
		sse.WriteString(interactionFrame(frame))
	}
	sse.WriteString("data: [DONE]\n\n")
	var request map[string]any
	provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		return interactionResponse(r, "text/event-stream", sse.String()), nil
	}})
	chunks, err := collectInteractions(t.Context(), provider, interactionRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	assistant := llm.Message{Role: llm.RoleAssistant}
	for _, chunk := range chunks {
		assistant.Content += chunk.Content
		assistant.ReasoningContent += chunk.Thinking
		assistant.ContinuationItems = append(assistant.ContinuationItems, chunk.CompletedItems...)
		if chunk.ContentOutputIndex != nil {
			assistant.ContentOutputIndex = chunk.ContentOutputIndex
		}
	}
	if assistant.Content != "answer" || assistant.ReasoningContent != "reason summary" || len(assistant.ContinuationItems) != 2 || !strings.Contains(string(assistant.ContinuationItems[0].Data), `"signature":"signed"`) || !strings.Contains(string(assistant.ContinuationItems[1].Data), "url_citation") {
		t.Fatalf("assistant=%+v", assistant)
	}
	final := chunks[len(chunks)-1]
	if final.Usage.PromptTokens != 3 || !final.Usage.ReasoningTokensReported {
		t.Fatalf("latest usage=%+v", final.Usage)
	}
	req := interactionRequest(true)
	req.Messages = append(req.Messages, assistant, llm.Message{Role: llm.RoleUser, Content: "next"})
	if _, err := collectInteractions(t.Context(), provider, req); err != nil {
		t.Fatal(err)
	}
	if len(request["input"].([]any)) != 4 {
		t.Fatalf("replay=%+v", request)
	}
	assistant.ContentOutputIndex = llm.OutputPosition(0)
	req.Messages[1] = assistant
	if _, err := collectInteractions(t.Context(), provider, req); !errors.Is(err, google.ErrInteractionsProtocol) {
		t.Fatalf("projection index=%v", err)
	}
}

func TestInteractionsNonstreamProtocolFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body string
		want       error
	}{
		{"error", `{"status":"failed","errors":[{"message":"secret-message","code":"secret-code"}]}`, google.ErrInteractionsProtocol},
		{"error-with-ok-status", `{"status":"completed","errors":[{"message":"secret-message","code":"secret-code"}]}`, google.ErrInteractionsProtocol},
		{"malformed", `{"secret":"unterminated`, llm.ErrProviderUnavailable},
		{"unknown-status", `{"status":"secret-status","steps":[]}`, google.ErrInteractionsProtocol},
		{"unsupported-step", `{"status":"completed","steps":[{"type":"secret-step"}]}`, llm.ErrModelNotSupported},
		{"duplicate-calls", `{"status":"requires_action","steps":[{"type":"function_call","id":"same","name":"echo","arguments":{}},{"type":"function_call","id":"same","name":"echo","arguments":{}}]}`, google.ErrInteractionsProtocol},
		{"no-calls", `{"status":"requires_action","steps":[]}`, google.ErrInteractionsProtocol},
		{"missing-text-steps", `{"status":"completed","steps":[],"output_text":"secret-text"}`, google.ErrInteractionsProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := &interactionBody{frames: []string{test.body}}
			provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				response := interactionResponse(r, "application/json", "")
				response.Body = body
				return response, nil
			}})
			chunks, err := collectInteractions(t.Context(), provider, interactionRequest(false))
			if !errors.Is(err, test.want) || !body.closed || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err=%v closed=%t", err, body.closed)
			}
			for _, chunk := range chunks {
				if len(chunk.ToolCalls) > 0 {
					t.Fatal("executable calls escaped invalid JSON response")
				}
			}
		})
	}
}

func TestInteractionsRequiredStreamIndexes(t *testing.T) {
	t.Parallel()
	frames := []string{
		`{"event_type":"step.start","index":0,"step":{"type":"function_call","id":"a","name":"echo","arguments":{}}}`,
		`{"event_type":"step.delta","index":0,"delta":{"type":"arguments_delta","arguments":"{}"}}`,
		`{"event_type":"step.stop","index":0}`,
	}
	for eventIndex, eventName := range []string{"start", "delta", "stop"} {
		for _, index := range []string{"", "null", "0.5", "0.0", "0e0", "-1", "9223372036854775808", `"secret-index"`, "true", "{}", `null,"INDEX":0`, `0,"INDEX":-1`, `null,"INDEX":0,"Index":0`, "0"} {
			t.Run(eventName+"/"+index, func(t *testing.T) {
				t.Parallel()
				var sse strings.Builder
				for i, frame := range frames {
					if i == eventIndex {
						if index == "" {
							frame = strings.Replace(frame, `,"index":0`, "", 1)
						} else {
							frame = strings.Replace(frame, `"index":0`, `"index":`+index, 1)
						}
					}
					sse.WriteString(interactionFrame(frame))
				}
				sse.WriteString(interactionFrame(`{"event_type":"interaction.completed","interaction":{"status":"requires_action"}}`) + "data: [DONE]\n\n")
				provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
					return interactionResponse(r, "text/event-stream", sse.String()), nil
				}})
				if index == "0" || index == `0,"INDEX":-1` {
					chunks, err := collectInteractions(t.Context(), provider, interactionRequest(true))
					if err != nil || len(chunks[len(chunks)-1].ToolCalls) != 1 {
						t.Fatalf("valid zero index: chunks=%+v err=%v", chunks, err)
					}
					return
				}
				assertInteractionsProtocolFailure(t, provider, interactionRequest(true))
			})
		}
	}
}

func assertInteractionsProtocolFailure(t *testing.T, provider llm.Provider, req llm.CompletionRequest) {
	t.Helper()
	errorsSeen := 0
	for chunk, err := range provider.Complete(t.Context(), req) {
		if len(chunk.ToolCalls) != 0 {
			t.Fatal("executable calls escaped malformed input")
		}
		if err == nil {
			if errorsSeen != 0 {
				t.Fatal("yield after terminal error")
			}
			continue
		}
		errorsSeen++
		if !errors.Is(err, google.ErrInteractionsProtocol) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("chunk=%+v err=%v", chunk, err)
		}
	}
	if errorsSeen != 1 {
		t.Fatalf("terminal errors=%d", errorsSeen)
	}
}

func TestInteractionsRequiredTextFields(t *testing.T) {
	t.Parallel()
	call := `{"type":"function_call","id":"a","name":"echo","arguments":{}}`
	for _, kind := range []string{"model_output", "thought"} {
		for _, source := range []string{"json", "start", "delta", "snapshot", "replay"} {
			for _, field := range []string{"", `,"text":null`, `,"text":17`, `,"text":true`, `,"text":{}`, `,"text":[]`, `,"TEXT":"secret-lost"`, `,"text":null,"TEXT":"secret-lost"`, `,"text":"","TEXT":"secret-replacement"`, `,"TEXT":"secret-replacement","text":""`, `,"text":" \t ","TEXT":null`, `,"text":""`, `,"text":" \t "`} {
				t.Run(kind+"/"+source+"/"+field, func(t *testing.T) {
					t.Parallel()
					valid := strings.Contains(field, `"text":""`) || strings.Contains(field, `"text":" \t "`)
					text := ""
					if strings.Contains(field, `"text":" \t "`) {
						text = " \t "
					}
					content := `{"type":"text"` + field + `}`
					property := "content"
					if kind == "thought" {
						property = "summary"
					}
					step := `{"type":"` + kind + `","` + property + `":[` + content + `]}`
					body := interactionSSE([]string{step, call}, "requires_action", "")
					contentType := "text/event-stream"
					switch source {
					case "json":
						body = `{"status":"requires_action","steps":[` + step + `,` + call + `]}`
						contentType = "application/json"
					case "delta":
						delta := `{"type":"text"` + field + `}`
						if kind == "thought" {
							delta = `{"type":"thought_summary","content":` + content + `}`
						}
						body = strings.Replace(body, `"step":`+step, `"step":{"type":"`+kind+`"}`, 1)
						body = strings.Replace(body, interactionFrame(`{"event_type":"step.stop","index":0}`), interactionFrame(`{"event_type":"step.delta","index":0,"delta":`+delta+`}`)+interactionFrame(`{"event_type":"step.stop","index":0}`), 1)
					case "snapshot":
						initial := step
						if !valid {
							initial = `{"type":"` + kind + `","` + property + `":[{"type":"text","text":""}]}`
						}
						body = interactionSSE([]string{initial, call}, "requires_action", "")
						body = strings.Replace(body, `"status":"requires_action"`, `"status":"requires_action","steps":[`+step+`,`+call+`]`, 1)
					}
					requests := 0
					var sent map[string]any
					provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
						requests++
						if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
							return nil, err
						}
						return interactionResponse(r, contentType, body), nil
					}})
					req := interactionRequest(source != "json")
					if source == "replay" {
						req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{{Provider: "google", Format: "interactions.step.v1", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"model":"` + interactionModel + `","step":` + step + `}`)}}})
					}
					before, _ := json.Marshal(req.Messages)
					if !valid {
						assertInteractionsProtocolFailure(t, provider, req)
						if source == "replay" && requests != 0 {
							t.Fatal("malformed replay reached HTTP")
						}
					} else {
						chunks, err := collectInteractions(t.Context(), provider, req)
						if err != nil {
							t.Fatal(err)
						}
						assistant := llm.Message{Role: llm.RoleAssistant}
						for _, chunk := range chunks {
							assistant.Content += chunk.Content
							assistant.ReasoningContent += chunk.Thinking
							assistant.ToolCalls = append(assistant.ToolCalls, chunk.ToolCalls...)
							assistant.ContinuationItems = append(assistant.ContinuationItems, chunk.CompletedItems...)
						}
						projection := assistant.Content
						if kind == "thought" {
							projection = assistant.ReasoningContent
						}
						if projection != text {
							t.Fatalf("projection=%q want=%q", projection, text)
						}
						replay := interactionRequest(req.Stream)
						replay.Messages = append(replay.Messages, assistant, llm.Message{Role: llm.RoleTool, ToolCallID: "a", Content: "result"})
						if _, err := collectInteractions(t.Context(), provider, replay); err != nil {
							t.Fatal(err)
						}
						var want any
						if err := json.Unmarshal([]byte(step), &want); err != nil {
							t.Fatal(err)
						}
						delete(want.(map[string]any)[property].([]any)[0].(map[string]any), "TEXT")
						if !reflect.DeepEqual(sent["input"].([]any)[1], want) {
							t.Fatalf("native text changed: input=%+v want=%+v", sent["input"], want)
						}
						assistant.Content = "secret-mismatch"
						replay.Messages[1] = assistant
						count := requests
						assertInteractionsProtocolFailure(t, provider, replay)
						if requests != count {
							t.Fatal("mismatched projection reached HTTP")
						}
					}
					after, _ := json.Marshal(req.Messages)
					if string(before) != string(after) {
						t.Fatal("borrowed history mutated")
					}
				})
			}
		}
	}
}

func TestInteractionsTerminalSnapshotExactNumbers(t *testing.T) {
	t.Parallel()
	start := `{"type":"function_call","id":"a","name":"echo","arguments":{"n":9007199254740993,"nested":[{"value":1.0}],"zero":-0}}`
	for _, test := range []struct {
		name, terminal string
		want           error
	}{
		{name: "absent"},
		{name: "identical", terminal: start},
		{name: "equivalent", terminal: `{"type":"function_call","id":"a","name":"echo","arguments":{"zero":0.0,"nested":[{"value":1e0}],"n":9.007199254740993e15}}`},
		{name: "rounded", terminal: strings.Replace(start, "9007199254740993", "9007199254740992", 1), want: google.ErrInteractionsProtocol},
		{name: "changed", terminal: strings.Replace(start, `"value":1.0`, `"value":2`, 1), want: google.ErrInteractionsProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := interactionSSE([]string{start}, "requires_action", "")
			if test.terminal != "" {
				body = strings.Replace(body, `"status":"requires_action"`, `"status":"requires_action","steps":[`+test.terminal+`]`, 1)
			}
			provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				return interactionResponse(r, "text/event-stream", body), nil
			}})
			if test.want != nil {
				assertInteractionsProtocolFailure(t, provider, interactionRequest(true))
				return
			}
			chunks, err := collectInteractions(t.Context(), provider, interactionRequest(true))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(chunks[len(chunks)-1].ToolCalls[0].Function.Arguments, "9007199254740993") || !strings.Contains(string(chunks[0].CompletedItems[0].Data), "9007199254740993") {
				t.Fatalf("exact starting data changed: %+v", chunks)
			}
		})
	}
}

func TestInteractionsCanonicalIndexOnly(t *testing.T) {
	t.Parallel()
	for _, field := range []string{`"INDEX":0`, `"index":null,"INDEX":0`} {
		for _, event := range []string{"start", "delta", "stop"} {
			t.Run(event+"/"+field, func(t *testing.T) {
				t.Parallel()
				body := fragmentedInteractions()
				prefix := `"event_type":"step.` + event + `","index":0`
				body = strings.Replace(body, prefix, `"event_type":"step.`+event+`",`+field, 1)
				provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
					return interactionResponse(r, "text/event-stream", body), nil
				}})
				assertInteractionsProtocolFailure(t, provider, interactionRequest(true))
			})
		}
	}
}

func TestInteractionsCanonicalArgumentsOnly(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"json", "start", "snapshot", "replay"} {
		for _, fields := range []string{
			`"ARGUMENTS":{"secret-replacement":42}`,
			`"arguments":null,"ARGUMENTS":{"secret-replacement":42}`,
			`"arguments":{"n":7},"ARGUMENTS":{"secret-replacement":42}`,
			`"ARGUMENTS":{"secret-replacement":42},"arguments":{"n":7}`,
			`"arguments":{"n":9007199254740993},"ARGUMENTS":{"secret-replacement":42}`,
		} {
			t.Run(source+"/"+fields, func(t *testing.T) {
				t.Parallel()
				valid := strings.Contains(fields, `"arguments":{"n":`)
				step := `{"type":"function_call","id":"a","name":"echo",` + fields + `}`
				body := interactionSSE([]string{step}, "requires_action", "")
				contentType := "text/event-stream"
				req := interactionRequest(source != "json")
				switch source {
				case "json":
					body = `{"status":"requires_action","steps":[` + step + `]}`
					contentType = "application/json"
				case "snapshot":
					initial := `{"type":"function_call","id":"a","name":"echo","arguments":{"n":7}}`
					if strings.Contains(fields, "9007199254740993") {
						initial = strings.Replace(initial, "7", "9007199254740993", 1)
					}
					body = interactionSSE([]string{initial}, "requires_action", "")
					body = strings.Replace(body, `"status":"requires_action"`, `"status":"requires_action","steps":[`+step+`]`, 1)
				case "replay":
					req.Messages = append(req.Messages,
						llm.Message{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{{Provider: "google", Format: "interactions.step.v1", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"model":"` + interactionModel + `","step":` + step + `}`)}}},
						llm.Message{Role: llm.RoleTool, ToolCallID: "a", Content: "result"})
					body = interactionSSE([]string{`{"type":"model_output","content":[{"type":"text","text":"answer"}]}`}, "completed", "")
				}
				requests := 0
				var sent string
				provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
					requests++
					data, err := io.ReadAll(r.Body)
					if err != nil {
						return nil, err
					}
					sent = string(data)
					return interactionResponse(r, contentType, body), nil
				}})
				if !valid {
					assertInteractionsProtocolFailure(t, provider, req)
					if source == "replay" && requests != 0 {
						t.Fatal("invalid native arguments reached HTTP")
					}
					return
				}
				chunks, err := collectInteractions(t.Context(), provider, req)
				if err != nil {
					t.Fatal(err)
				}
				want := `{"n":7}`
				if strings.Contains(fields, "9007199254740993") {
					want = `{"n":9007199254740993}`
				}
				if source == "replay" {
					if !strings.Contains(sent, `"arguments":`+want) || strings.Contains(sent, "secret-replacement") {
						t.Fatalf("native arguments changed: %s", sent)
					}
				} else if calls := chunks[len(chunks)-1].ToolCalls; len(calls) != 1 || calls[0].Function.Arguments != want {
					t.Fatalf("canonical arguments changed: %+v", calls)
				}
			})
		}
	}
}

func TestInteractionsCanonicalStructuralKeys(t *testing.T) {
	t.Parallel()
	step := `{"type":"model_output","content":[{"type":"text","text":"answer"}]}`
	thought := `{"type":"thought","summary":[{"type":"text","text":"answer"}]}`
	for _, test := range []struct {
		name, source, body string
		want               error
		thinking           bool
	}{
		{name: "steps", source: "json", body: `{"status":"completed","steps":[` + step + `],"STEPS":[{"type":"text","text":"secret-replacement"}]}`},
		{name: "content", source: "json", body: `{"status":"completed","steps":[` + strings.TrimSuffix(step, "}") + `,"CONTENT":[{"type":"text","text":null}]}]}`},
		{name: "summary", source: "json", body: `{"status":"completed","steps":[` + strings.TrimSuffix(thought, "}") + `,"SUMMARY":[{"type":"text","text":null}]}]}`, thinking: true},
		{name: "step", body: strings.Replace(interactionSSE([]string{step}, "completed", ""), `"step":`+step, `"step":`+step+`,"STEP":{"type":"function_call","id":"secret-call","name":"echo","arguments":{}}`, 1)},
		{name: "delta", body: interactionFrame(`{"event_type":"step.start","index":0,"step":{"type":"model_output"}}`) + interactionFrame(`{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"answer"},"DELTA":{"type":"text","text":null}}`) + interactionFrame(`{"event_type":"step.stop","index":0}`) + interactionFrame(`{"event_type":"interaction.completed","interaction":{"status":"completed"}}`) + "data: [DONE]\n\n"},
		{name: "interaction", body: strings.Replace(interactionSSE([]string{step}, "completed", ""), `"status":"completed"}}`, `"status":"completed"},"INTERACTION":{"steps":[{"type":"model_output","content":[{"type":"text","text":null}]}]}}`, 1)},
		{name: "terminal-steps", body: strings.Replace(interactionSSE([]string{step}, "completed", ""), `"status":"completed"`, `"status":"completed","steps":[`+step+`],"STEPS":[{"type":"model_output","content":[{"type":"text","text":null}]}]`, 1)},
		{name: "native-model", source: "replay", body: `{"model":"` + interactionModel + `","MODEL":"secret-model","step":` + step + `}`},
		{name: "native-step", source: "replay", body: `{"model":"` + interactionModel + `","step":` + step + `,"STEP":{"type":"model_output","content":[{"type":"text","text":null}]}}`},
		{name: "native-model-missing", source: "replay", body: `{"MODEL":"` + interactionModel + `","step":` + step + `}`, want: google.ErrInteractionsProtocol},
		{name: "native-model-null", source: "replay", body: `{"model":null,"MODEL":"` + interactionModel + `","step":` + step + `}`, want: google.ErrInteractionsProtocol},
		{name: "native-step-missing", source: "replay", body: `{"model":"` + interactionModel + `","STEP":` + step + `}`, want: google.ErrInteractionsProtocol},
		{name: "native-step-null", source: "replay", body: `{"model":"` + interactionModel + `","step":null,"STEP":` + step + `}`, want: google.ErrInteractionsProtocol},
		{name: "type-collision", source: "replay", body: `{"model":"` + interactionModel + `","step":{"type":"model_output","TYPE":"function_call","id":"secret-call","name":"echo","arguments":{}}}`, want: google.ErrInteractionsProtocol},
		{name: "type-missing", source: "replay", body: `{"model":"` + interactionModel + `","step":{"TYPE":"function_call","id":"secret-call","name":"echo","arguments":{}}}`, want: google.ErrInteractionsProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := test.body
			contentType := "text/event-stream"
			req := interactionRequest(test.source != "json")
			if test.source == "json" {
				contentType = "application/json"
			}
			if test.source == "replay" {
				req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, Content: "answer", ContinuationItems: []llm.ContinuationItem{{Provider: "google", Format: "interactions.step.v1", OutputIndex: llm.OutputPosition(0), Data: []byte(test.body)}}})
				body = interactionSSE([]string{step}, "completed", "")
			}
			requests := 0
			provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
				requests++
				return interactionResponse(r, contentType, body), nil
			}})
			if test.want != nil {
				assertInteractionsProtocolFailure(t, provider, req)
				if test.source == "replay" && requests != 0 {
					t.Fatal("invalid native envelope reached HTTP")
				}
				return
			}
			chunks, err := collectInteractions(t.Context(), provider, req)
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			for _, chunk := range chunks {
				if len(chunk.ToolCalls) != 0 {
					t.Fatal("unknown case key supplied executable call")
				}
				if test.thinking {
					text.WriteString(chunk.Thinking)
				} else {
					text.WriteString(chunk.Content)
				}
			}
			if text.String() != "answer" {
				t.Fatalf("canonical projection changed: %q", text.String())
			}
		})
	}
}

func TestInteractionsCasefoldCannotHideCanonicalText(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"model_output", "thought"} {
		for _, source := range []string{"json", "start", "delta", "snapshot", "replay"} {
			t.Run(kind+"/"+source, func(t *testing.T) {
				t.Parallel()
				property := "content"
				if kind == "thought" {
					property = "summary"
				}
				step := `{"type":"` + kind + `","` + property + `":[{"type":"text","text":null}],"` + strings.ToUpper(property) + `":[{"type":"text","text":"secret-hidden"}]}`
				body := interactionSSE([]string{step}, "completed", "")
				contentType := "text/event-stream"
				req := interactionRequest(source != "json")
				switch source {
				case "json":
					body = `{"status":"completed","steps":[` + step + `]}`
					contentType = "application/json"
				case "delta":
					delta := `{"type":"text","text":null,"TEXT":"secret-hidden"}`
					if kind == "thought" {
						delta = `{"type":"thought_summary","content":{"type":"text","text":null},"CONTENT":{"type":"text","text":"secret-hidden"}}`
					}
					body = interactionFrame(`{"event_type":"step.start","index":0,"step":{"type":"`+kind+`"}}`) + interactionFrame(`{"event_type":"step.delta","index":0,"delta":`+delta+`}`) + interactionFrame(`{"event_type":"step.stop","index":0}`) + interactionFrame(`{"event_type":"interaction.completed","interaction":{"status":"completed"}}`) + "data: [DONE]\n\n"
				case "snapshot":
					body = interactionSSE([]string{`{"type":"` + kind + `","` + property + `":[{"type":"text","text":""}]}`}, "completed", "")
					body = strings.Replace(body, `"status":"completed"`, `"status":"completed","steps":[`+step+`]`, 1)
				case "replay":
					req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{{Provider: "google", Format: "interactions.step.v1", OutputIndex: llm.OutputPosition(0), Data: []byte(`{"model":"` + interactionModel + `","step":` + step + `}`)}}})
				}
				requests := 0
				provider := interactionProvider(t, &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
					requests++
					return interactionResponse(r, contentType, body), nil
				}})
				assertInteractionsProtocolFailure(t, provider, req)
				if source == "replay" && requests != 0 {
					t.Fatal("invalid canonical text reached HTTP")
				}
			})
		}
	}
}
