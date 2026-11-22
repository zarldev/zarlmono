package google_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"cloud.google.com/go/auth"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestClientHookUserOnlyOverrides(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, withHook := range []bool{false, true} {
				for _, override := range []string{"", `[{"role":"user","parts":[{"text":"override"}],"callerField":true}]`} {
					t.Run(route.name+"/"+override+"/hook="+strconv.FormatBool(withHook), func(t *testing.T) {
						t.Parallel()
						var extra map[string]any
						if override != "" {
							// Ordinary json.Unmarshal produces []any, not the SDK's
							// native []map[string]any contents representation.
							extra = make(map[string]any)
							var contents any
							if err := json.Unmarshal([]byte(override), &contents); err != nil {
								t.Fatal(err)
							}
							extra["contents"] = contents
						}
						calls := 0
						var hook genai.ExtrasRequestProvider
						if withHook {
							hook = func(body map[string]any) map[string]any {
								calls++
								body["callerPolicy"] = "enforced"
								return body
							}
						}
						provider, client, requests := hookRouteProvider(t, route, extra, hook)
						before := marshalHookValue(t, client.ClientConfig().HTTPOptions.ExtraBody)
						collectNativeAssistant(t, provider, []llm.Message{{Role: llm.RoleUser, Content: "original"}})
						request := <-requests
						var body map[string]any
						decodeNativeTestJSON(t, request, &body)
						wantCalls := 0
						if withHook {
							wantCalls = 1
							if body["callerPolicy"] != "enforced" {
								t.Fatalf("caller policy missing: %s", request)
							}
						}
						if calls != wantCalls {
							t.Fatalf("hook calls = %d, want %d", calls, wantCalls)
						}
						wantText := "original"
						if override != "" {
							wantText = "override"
						}
						if got := nativeRequestParts(t, request, 0)[0]["text"]; got != wantText {
							t.Fatalf("user text = %v, want %s", got, wantText)
						}
						if after := marshalHookValue(t, client.ClientConfig().HTTPOptions.ExtraBody); !bytes.Equal(before, after) {
							t.Fatal("request mutated client ExtraBody")
						}
					})
				}
			}
		})
	}
}

func TestClientHookNativeRestorationAndFinalAuthority(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, customizeHistory := range []bool{false, true} {
				t.Run(strconv.FormatBool(customizeHistory), func(t *testing.T) {
					t.Parallel()
					messages := hookNativeMessages()
					calls := 0
					hook := func(body map[string]any) map[string]any {
						calls++
						// Policy sees exact restored numbers, not SDK-rounded data.
						data := marshalHookValue(t, body)
						assertHookNativeParts(t, data)
						if customizeHistory {
							body["contents"] = []map[string]any{{"role": "user", "parts": []map[string]any{{"text": "policy replacement"}}}}
						}
						// Returning a replacement map is supported too.
						return map[string]any{"contents": body["contents"], "callerPolicy": "final"}
					}
					provider, _, requests := hookRouteProvider(t, route, nil, hook)
					collectNativeAssistant(t, provider, messages)
					request := <-requests
					if calls != 1 {
						t.Fatalf("hook calls = %d, want 1", calls)
					}
					var body map[string]any
					decodeNativeTestJSON(t, request, &body)
					if body["callerPolicy"] != "final" {
						t.Fatalf("caller policy missing: %s", request)
					}
					if customizeHistory {
						if nativeRequestParts(t, request, 0)[0]["text"] != "policy replacement" {
							t.Fatalf("restoration undid final caller customization: %s", request)
						}
					} else {
						assertHookNativeParts(t, request)
					}
				})
			}
		})
	}
}

func TestClientHookNestedNativeEdits(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, stream := range []bool{false, true} {
				for _, override := range []bool{false, true} {
					t.Run("stream="+strconv.FormatBool(stream)+"/override="+strconv.FormatBool(override), func(t *testing.T) {
						t.Parallel()
						messages := hookNativeMessages()
						beforeMessages := marshalHookValue(t, messages)
						var extra map[string]any
						if override {
							extra = map[string]any{"contents": []map[string]any{{"role": "user"}, {"role": "model"}, {"role": "model"}, {"role": "user"}}}
						}
						beforeExtra := marshalHookValue(t, extra)
						calls := 0
						provider, client, requests := hookRouteProvider(t, route, extra, func(body map[string]any) map[string]any {
							calls++
							assertHookNativeParts(t, marshalHookValue(t, body))
							// Existing SDK hooks navigate these nested map slices on
							// both backends; typed []*genai.Part is not compatible.
							contents := body["contents"].([]map[string]any)
							parts, ok := contents[1]["parts"].([]map[string]any)
							if !ok {
								t.Fatalf("model parts type = %T, want []map[string]any", contents[1]["parts"])
							}
							call := parts[0]["functionCall"].(map[string]any)
							args := call["args"].(map[string]any)
							if args["n"] != json.Number("9007199254740993") || args["fraction"] != json.Number("0.10000000000000001") {
								t.Fatalf("hook numbers = %#v", args)
							}
							args["policy"] = "edited"
							call["name"] = "policy_read"
							parts[0]["thoughtSignature"] = "cG9saWN5"
							contents[2]["parts"].([]map[string]any)[0]["text"] = "policy text"
							return body
						})
						for range 2 {
							for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages, Stream: stream}) {
								if err != nil {
									t.Fatal(err)
								}
							}
							request := <-requests
							part := nativeRequestParts(t, request, 1)[0]
							call := part["functionCall"].(map[string]any)
							wantArgs := map[string]any{"n": json.Number("9007199254740993"), "fraction": json.Number("0.10000000000000001"), "policy": "edited"}
							if diff := cmp.Diff(wantArgs, call["args"]); diff != "" {
								t.Fatalf("nested hook edit (-want +got):\n%s", diff)
							}
							if call["name"] != "policy_read" || part["thoughtSignature"] != "cG9saWN5" || nativeRequestParts(t, request, 2)[0]["text"] != "policy text" {
								t.Fatalf("restoration undid nested caller edits: %s", request)
							}
						}
						if calls != 2 {
							t.Fatalf("hook calls = %d, want 2", calls)
						}
						if !bytes.Equal(beforeMessages, marshalHookValue(t, messages)) || !bytes.Equal(beforeExtra, marshalHookValue(t, client.ClientConfig().HTTPOptions.ExtraBody)) {
							t.Fatal("nested hook edits mutated borrowed messages or client ExtraBody")
						}
					})
				}
			}
		})
	}
}

func TestClientHookRunsPerSDKAttempt(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, stream := range []bool{false, true} {
				for _, native := range []bool{false, true} {
					t.Run("stream="+strconv.FormatBool(stream)+"/native="+strconv.FormatBool(native), func(t *testing.T) {
						t.Parallel()
						synctest.Test(t, func(t *testing.T) {
							messages := []llm.Message{{Role: llm.RoleUser, Content: "start"}}
							if native {
								messages = hookNativeMessages()
							}
							beforeMessages := marshalHookValue(t, messages)
							extra := map[string]any{"policy": map[string]any{"value": "original"}}
							beforeExtra := marshalHookValue(t, extra)
							calls, requests := 0, 0
							transport := &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
								requests++
								data, err := io.ReadAll(r.Body)
								if err != nil {
									return nil, err
								}
								var body map[string]any
								decodeNativeTestJSON(t, data, &body)
								wantCall := json.Number("1")
								if requests == 3 {
									wantCall = "2"
								}
								if body["hookCall"] != wantCall {
									t.Fatalf("HTTP attempt %d hook call = %v, want %s", requests, body["hookCall"], wantCall)
								}
								status := http.StatusOK
								contentType := "application/json"
								response := `{"candidates":[{"finishReason":"STOP"}]}`
								if requests <= 2 {
									status = http.StatusTooManyRequests
									response = `{"error":{"code":429,"message":"retry","status":"RESOURCE_EXHAUSTED"}}`
								} else if stream {
									contentType = "text/event-stream"
									response = "data: " + response + "\n\n"
								}
								return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
							}}
							cfg := &genai.ClientConfig{
								Backend: route.backend, HTTPClient: &http.Client{Transport: transport},
								HTTPOptions: genai.HTTPOptions{
									ExtraBody: extra,
									// Exhaust SDK transport retries once, then let the
									// provider construct a fresh SDK attempt. Fake time
									// advances through both retry layers without sleeps.
									RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr[int32](2), Jitter: genai.Ptr(0.0)},
									ExtrasRequestProvider: func(body map[string]any) map[string]any {
										calls++
										if native {
											assertHookNativeParts(t, marshalHookValue(t, body))
											contents := body["contents"].([]map[string]any)
											parts := contents[1]["parts"].([]map[string]any)
											parts[0]["functionCall"].(map[string]any)["args"].(map[string]any)["policy"] = "attempt-owned"
										}
										policy := body["policy"].(map[string]any)
										if policy["value"] != "original" {
											t.Fatal("hook received previous attempt's mutation")
										}
										policy["value"] = "attempt-owned"
										body["hookCall"] = calls
										return body
									},
								},
							}
							if route.backend == genai.BackendVertexAI {
								cfg.Project, cfg.Location = "test-project", "test-location"
								cfg.Credentials = &auth.Credentials{}
							} else {
								cfg.APIKey = "test-key"
							}
							client, err := genai.NewClient(t.Context(), cfg)
							if err != nil {
								t.Fatal(err)
							}
							provider, err := google.NewProvider("", google.WithClient(client))
							if err != nil {
								t.Fatal(err)
							}
							completion := provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages, Stream: stream})
							if calls != 0 || requests != 0 {
								t.Fatal("Complete was not lazy")
							}
							for _, err := range completion {
								if err != nil {
									t.Fatal(err)
								}
							}
							if calls != 2 || requests != 3 {
								t.Fatalf("hook calls=%d HTTP attempts=%d, want 2 and 3", calls, requests)
							}
							if !bytes.Equal(beforeMessages, marshalHookValue(t, messages)) || !bytes.Equal(beforeExtra, marshalHookValue(t, extra)) {
								t.Fatal("retry hook mutated borrowed messages or client ExtraBody")
							}
						})
					})
				}
			}
		})
	}
}

func TestClientHookAutomaticContinuationIsolation(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []struct {
				name       string
				stream     bool
				checkpoint bool
			}{
				{name: "nonstream"},
				{name: "stream", stream: true},
				{name: "checkpoint_reconnect", stream: true, checkpoint: true},
			} {
				for _, override := range []bool{false, true} {
					t.Run(mode.name+"/override="+strconv.FormatBool(override), func(t *testing.T) {
						t.Parallel()
						messages := hookNativeMessages()
						messages[3].ContinuationItems[0].Data = []byte(`{"functionCall":{"id":"call","name":"read","args":{"n":9007199254740993,"fraction":0.10000000000000001,"nested":[{"value":"original"}]}},"thoughtSignature":"c2ln"}`)
						extra := map[string]any{"policy": map[string]any{
							"value":  "original",
							"nested": []any{map[string]any{"value": "original", "n": json.Number("9007199254740993")}},
						}}
						if override {
							extra["contents"] = []map[string]any{{"role": "user"}, {"role": "model"}, {"role": "model"}, {"role": "user"}}
						}
						beforeMessages, beforeExtra := marshalHookValue(t, messages), marshalHookValue(t, extra)
						calls, requests := 0, 0
						transport := &recordingTransport{roundTrip: func(r *http.Request) (*http.Response, error) {
							requests++
							data, err := io.ReadAll(r.Body)
							if err != nil {
								return nil, err
							}
							var body map[string]any
							decodeNativeTestJSON(t, data, &body)
							edit := "edit" + strconv.Itoa(requests)
							args := nativeRequestParts(t, data, 1)[0]["functionCall"].(map[string]any)["args"].(map[string]any)
							policy := body["policy"].(map[string]any)
							if args["policy"] != edit || args["nested"].([]any)[0].(map[string]any)["value"] != edit || policy["value"] != edit || policy["nested"].([]any)[0].(map[string]any)["value"] != edit {
								t.Fatalf("hop %d lost final caller edits: %s", requests, data)
							}
							if requests > 2 {
								t.Fatalf("unexpected SDK hop %d", requests)
							}
							response := `{"candidates":[{"content":{"role":"model","parts":[{"text":"last"}]},"finishReason":"STOP"}]}`
							if requests == 1 {
								response = `{"candidates":[{"content":{"role":"model","parts":[{"text":"first"}]},"finishReason":"CONTINUATION","continuationToken":"aG9w"}]}`
								if mode.checkpoint {
									// EOF after a checkpoint (without a terminal reason) makes
									// the SDK reconnect through the same captured hook.
									response = `{"candidates":[{"content":{"role":"model","parts":[{"text":"first"}]},"continuationToken":"aG9w"}]}`
								}
							}
							contentType := "application/json"
							if mode.stream {
								contentType = "text/event-stream"
								response = "data: " + response + "\n\n"
							}
							return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
						}}
						cfg := &genai.ClientConfig{
							Backend: route.backend, HTTPClient: &http.Client{Transport: transport},
							HTTPOptions: genai.HTTPOptions{ExtraBody: extra, ExtrasRequestProvider: func(body map[string]any) map[string]any {
								calls++
								contents := body["contents"].([]map[string]any)
								parts := contents[1]["parts"].([]map[string]any)
								args := parts[0]["functionCall"].(map[string]any)["args"].(map[string]any)
								policy := body["policy"].(map[string]any)
								if args["policy"] != nil || args["nested"].([]any)[0].(map[string]any)["value"] != "original" {
									t.Errorf("hook hop %d inherited model-part mutation: %#v", calls, args)
								}
								if policy["value"] != "original" || policy["nested"].([]any)[0].(map[string]any)["value"] != "original" {
									t.Errorf("hook hop %d inherited static policy mutation: %#v", calls, policy)
								}
								if args["n"] != json.Number("9007199254740993") || args["fraction"] != json.Number("0.10000000000000001") || policy["nested"].([]any)[0].(map[string]any)["n"] != json.Number("9007199254740993") {
									t.Fatalf("hook hop %d lost exact numbers", calls)
								}
								if calls == 1 && body["continuationToken"] != nil || calls == 2 && body["continuationToken"] != "aG9w" {
									t.Fatalf("hook hop %d lost newly generated SDK input: %#v", calls, body["continuationToken"])
								}
								edit := "edit" + strconv.Itoa(calls)
								args["policy"] = edit
								args["nested"].([]any)[0].(map[string]any)["value"] = edit
								policy["value"] = edit
								policy["nested"].([]any)[0].(map[string]any)["value"] = edit
								// A replacement body remains authoritative on every hop.
								return map[string]any{"contents": contents, "policy": policy, "continuationToken": body["continuationToken"]}
							}},
						}
						if route.backend == genai.BackendVertexAI {
							cfg.Project, cfg.Location = "test-project", "test-location"
							cfg.Credentials = &auth.Credentials{}
						} else {
							cfg.APIKey = "test"
						}
						client, err := genai.NewClient(t.Context(), cfg)
						if err != nil {
							t.Fatal(err)
						}
						provider, err := google.NewProvider("", google.WithClient(client))
						if err != nil {
							t.Fatal(err)
						}
						for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages, Stream: mode.stream}) {
							if err != nil {
								t.Fatal(err)
							}
						}
						if calls != 2 || requests != 2 {
							t.Fatalf("hook calls=%d HTTP requests=%d, want 2 and 2", calls, requests)
						}
						if !bytes.Equal(beforeMessages, marshalHookValue(t, messages)) || !bytes.Equal(beforeExtra, marshalHookValue(t, extra)) {
							t.Fatal("SDK continuation mutated borrowed messages or client ExtraBody")
						}
					})
				}
			}
		})
	}
}

func TestClientHookCompatibleNativeContentsOverrides(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, typed := range []bool{false, true} {
				t.Run(strconv.FormatBool(typed), func(t *testing.T) {
					t.Parallel()
					// One model part set is equivalent with different numeric
					// spelling; the other is omitted for restoration to supply.
					data := `[{"role":"user","parts":[{"text":"user override"}],"userField":true},{"role":"model","parts":[{"functionCall":{"id":"call","name":"read","args":{"n":9007199254740993.0,"fraction":1.0000000000000001e-1}},"thoughtSignature":"c2ln"}],"modelField":{"n":9007199254740993}},{"role":"model","modelField":"kept"},{"role":"user","parts":[{"text":"end override"}]}]`
					var contents any
					if typed {
						var maps []map[string]any
						decodeNativeTestJSON(t, []byte(data), &maps)
						contents = maps
					} else {
						decodeNativeTestJSON(t, []byte(data), &contents)
					}
					extra := map[string]any{"contents": contents, "staticPolicy": map[string]any{"n": json.Number("9007199254740993")}}
					before := marshalHookValue(t, extra)
					calls := 0
					hook := func(body map[string]any) map[string]any {
						calls++
						body["callerPolicy"] = "kept"
						return body
					}
					provider, client, requests := hookRouteProvider(t, route, extra, hook)
					for range 2 {
						collectNativeAssistant(t, provider, hookNativeMessages())
						request := <-requests
						assertHookNativeParts(t, request)
						var body struct {
							Contents     []map[string]any `json:"contents"`
							StaticPolicy map[string]any   `json:"staticPolicy"`
							CallerPolicy string           `json:"callerPolicy"`
						}
						decodeNativeTestJSON(t, request, &body)
						if body.CallerPolicy != "kept" || body.Contents[2]["modelField"] != "kept" || body.Contents[0]["userField"] != true {
							t.Fatalf("caller fields erased: %s", request)
						}
						wantNumbers := map[string]any{"n": json.Number("9007199254740993")}
						if diff := cmp.Diff(wantNumbers, body.StaticPolicy); diff != "" {
							t.Fatalf("static policy changed (-want +got):\n%s", diff)
						}
						if diff := cmp.Diff(wantNumbers, body.Contents[1]["modelField"]); diff != "" {
							t.Fatalf("model field changed (-want +got):\n%s", diff)
						}
						if nativeRequestParts(t, request, 0)[0]["text"] != "user override" || nativeRequestParts(t, request, 3)[0]["text"] != "end override" {
							t.Fatalf("user override changed: %s", request)
						}
					}
					if calls != 2 {
						t.Fatalf("hook calls = %d, want 2", calls)
					}
					if after := marshalHookValue(t, client.ClientConfig().HTTPOptions.ExtraBody); !bytes.Equal(before, after) {
						t.Fatal("restoration mutated shared client ExtraBody")
					}
				})
			}
		})
	}
}

func TestClientHookRejectsIncompatibleNativeContentsBeforeDispatch(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, override := range []string{
				`null`, `"override-canary"`, `{}`, `[]`,
				`[{"role":"model"}]`,
				`[{"role":"user"},{"role":"model"},{"role":"model"},null]`,
				`[{"role":"user"},{"role":"model"},{"role":"model"},{"parts":[{"text":"override-canary"}]}]`,
				`[{"role":"model"},{"role":"user"},{"role":"model"},{"role":"user"}]`,
				`[{"role":"user"},{"role":"model","parts":[{"text":"override-canary"}]},{"role":"model"},{"role":"user"}]`,
				`[{"role":"user"},{"role":"model"},{"role":"model","parts":null},{"role":"user"}]`,
				`[{"role":"user"},{"role":"model","parts":[{"functionCall":{"id":"call","name":"read","args":{"n":9007199254740992,"fraction":0.10000000000000001}},"thoughtSignature":"c2ln"}]},{"role":"model"},{"role":"user"}]`,
				`[{"role":"user"},{"role":["model"]},{"role":"model"},{"role":"user"}]`,
				`[{"role":"user"},{"role":"model","parts":[{"text":"after","thoughtSignature":"YWZ0ZXI="}]},{"role":"model"},{"role":"user"}]`,
			} {
				t.Run(override, func(t *testing.T) {
					t.Parallel()
					var contents any
					decodeNativeTestJSON(t, []byte(override), &contents)
					calls := 0
					provider, _, requests := hookRouteProvider(t, route, map[string]any{"contents": contents}, func(body map[string]any) map[string]any {
						calls++
						return body
					})
					stream := provider.Complete(t.Context(), llm.CompletionRequest{Messages: hookNativeMessages()})
					if calls != 0 {
						t.Fatal("Complete was not lazy")
					}
					yields := 0
					for chunk, err := range stream {
						yields++
						if !errors.Is(err, google.ErrIncompatibleContentsOverride) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
							t.Fatalf("yield = %#v, %v", chunk, err)
						}
						if strings.Contains(err.Error(), "override-canary") || strings.Contains(err.Error(), "9007199254740993") {
							t.Fatalf("error exposed override/native data: %v", err)
						}
					}
					if yields != 1 || calls != 0 {
						t.Fatalf("yields=%d hook calls=%d", yields, calls)
					}
					select {
					case request := <-requests:
						t.Fatalf("incompatible override dispatched HTTP: %s", request)
					default:
					}
				})
			}
		})
	}
}

func TestClientHookSharedClientDoesNotMutateExtraBody(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			extra := map[string]any{"contents": []map[string]any{{"role": "user"}, {"role": "model"}, {"role": "model"}, {"role": "user"}}, "policy": map[string]any{"value": "original"}}
			before := marshalHookValue(t, extra)
			provider, client, requests := hookRouteProvider(t, route, extra, func(body map[string]any) map[string]any {
				calls.Add(1)
				policy, ok := body["policy"].(map[string]any)
				if !ok || policy["value"] != "original" {
					t.Error("hook did not receive independent policy snapshot")
				}
				policy["value"] = "request-owned"
				return body
			})
			other, err := google.NewProvider("", google.WithClient(client))
			if err != nil {
				t.Fatal(err)
			}
			var workers sync.WaitGroup
			for _, p := range []*google.Provider{provider, other, provider, other} {
				workers.Go(func() {
					for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Messages: hookNativeMessages()}) {
						if err != nil {
							t.Error(err)
						}
					}
				})
			}
			workers.Wait()
			if calls.Load() != 4 || len(requests) != 4 {
				t.Fatalf("hook calls=%d HTTP requests=%d", calls.Load(), len(requests))
			}
			if after := marshalHookValue(t, extra); !bytes.Equal(before, after) {
				t.Fatal("concurrent requests mutated shared ExtraBody")
			}
		})
	}
}

func hookNativeMessages() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser}, // Skipped during conversion: override indices are wire turn indices.
		{Role: llm.RoleUser, Content: "start"},
		{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{nativePartItem(0, `{"functionCall":{"id":"call","name":"read","args":{"n":9007199254740993,"fraction":0.10000000000000001}},"thoughtSignature":"c2ln"}`)}},
		{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{nativePartItem(0, `{"text":"after","thoughtSignature":"YWZ0ZXI="}`)}},
		{Role: llm.RoleUser, Content: "end"},
	}
}

func assertHookNativeParts(t *testing.T, request []byte) {
	t.Helper()
	messages := hookNativeMessages()
	for _, binding := range []struct{ message, turn int }{{3, 1}, {4, 2}} {
		var want map[string]any
		decodeNativeTestJSON(t, messages[binding.message].ContinuationItems[0].Data, &want)
		if diff := cmp.Diff([]map[string]any{want}, nativeRequestParts(t, request, binding.turn)); diff != "" {
			t.Fatalf("native turn %d (-want +got):\n%s", binding.turn, diff)
		}
	}
}

func marshalHookValue(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func hookRouteProvider(t *testing.T, route googleRoute, extra map[string]any, hook genai.ExtrasRequestProvider) (*google.Provider, *genai.Client, <-chan []byte) {
	t.Helper()
	requests := make(chan []byte, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		requests <- body
		response := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
		if strings.Contains(r.URL.Path, ":streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: "+response+"\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	cfg := &genai.ClientConfig{
		Backend:     route.backend,
		HTTPClient:  server.Client(),
		HTTPOptions: genai.HTTPOptions{BaseURL: server.URL, ExtraBody: extra, ExtrasRequestProvider: hook},
	}
	if route.backend == genai.BackendVertexAI {
		cfg.Project = "test-project"
		cfg.Location = "test-location"
		cfg.Credentials = &auth.Credentials{}
	} else {
		cfg.APIKey = "test-key"
	}
	client, err := genai.NewClient(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := google.NewProvider("", google.WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	return provider, client, requests
}
