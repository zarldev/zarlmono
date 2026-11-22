package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestProviderSignedFunctionCallsCaptureAndReplayInOrder(t *testing.T) {
	var requests [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, append([]byte(nil), body...))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"read\",\"args\":{\"path\":\"foo.go\"}},\"thoughtSignature\":\"c2lnbmVk\"},{\"functionCall\":{\"name\":\"write\",\"args\":{\"path\":\"bar.go\"}}}]},\"finishReason\":\"STOP\"}]}\n\n"))
	}))
	defer server.Close()

	p, err := google.NewProvider("test-key", google.WithBaseURL(server.URL), google.WithModel("gemini-3.8-flash"))
	if err != nil {
		t.Fatal(err)
	}
	var calls []llm.ToolCall
	var items []llm.ContinuationItem
	for chunk, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "read and write"}}}) {
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, chunk.ToolCalls...)
		items = append(items, chunk.Clone().CompletedItems...)
	}
	if len(calls) != 2 || calls[0].Function.Name != "read" || calls[1].Function.Name != "write" {
		t.Fatalf("calls=%#v, want read then write", calls)
	}
	if calls[0].ID == calls[1].ID || calls[0].OutputIndex == nil || *calls[0].OutputIndex != 0 ||
		calls[1].OutputIndex == nil || *calls[1].OutputIndex != 1 {
		t.Fatalf("call pairing metadata=%#v", calls)
	}
	if len(items) != 2 || items[0].ID != calls[0].ID || items[0].OutputIndex == nil || *items[0].OutputIndex != 0 ||
		items[1].ID != calls[1].ID || items[1].OutputIndex == nil || *items[1].OutputIndex != 1 {
		t.Fatalf("items=%#v, want both native calls", items)
	}

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "read and write"},
		{Role: llm.RoleAssistant, ToolCalls: calls, ContinuationItems: items},
		{Role: llm.RoleTool, ToolCallID: calls[0].ID, Content: "read ok"},
		{Role: llm.RoleTool, ToolCallID: calls[1].ID, Content: "write ok"},
	}
	for _, err := range p.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: messages}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}

	var request struct {
		Contents []struct {
			Role  string           `json:"role"`
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(requests[1], &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Contents) != 3 || len(request.Contents[1].Parts) != 2 {
		t.Fatalf("replayed contents=%s", requests[1])
	}
	if request.Contents[2].Role != llm.RoleUser || len(request.Contents[2].Parts) != 2 {
		t.Fatalf("parallel function responses were not grouped in call order: %s", requests[1])
	}
	for index, name := range []string{"read", "write"} {
		response, ok := request.Contents[2].Parts[index]["functionResponse"].(map[string]any)
		if !ok || response["name"] != name {
			t.Fatalf("function response %d = %#v, want %s", index, request.Contents[2].Parts[index], name)
		}
	}
	parts := request.Contents[1].Parts
	first, ok := parts[0]["functionCall"].(map[string]any)
	if !ok || first["name"] != "read" || parts[0]["thoughtSignature"] != "c2lnbmVk" {
		t.Fatalf("first replayed part=%#v", parts[0])
	}
	second, ok := parts[1]["functionCall"].(map[string]any)
	if !ok || second["name"] != "write" {
		t.Fatalf("second replayed part=%#v", parts[1])
	}
	if _, duplicated := parts[1]["thoughtSignature"]; duplicated {
		t.Fatalf("signature duplicated onto unsigned call: %#v", parts[1])
	}
}

func TestNativeContinuationRejectsConflictingCallBindings(t *testing.T) {
	t.Parallel()
	part := nativePartItem(0, `{"functionCall":{"id":"call","name":"read","args":{"path":"native-canary"}},"thoughtSignature":"c2ln"}`)
	part.ID = "call"
	repeated := part.Clone()
	repeated.OutputIndex = llm.OutputPosition(1)
	anonymous := nativePartItem(0, `{"functionCall":{"name":"read","args":{"path":"native-canary"}},"thoughtSignature":"c2ln"}`)
	repeatedAnonymous := anonymous.Clone()
	repeatedAnonymous.OutputIndex = llm.OutputPosition(1)
	call := llm.ToolCall{ID: "call", OutputIndex: llm.OutputPosition(0), Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"native-canary"}`}}
	for _, test := range []struct {
		name  string
		items []llm.ContinuationItem
		calls []llm.ToolCall
	}{
		{name: "stale arguments and position", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "call", OutputIndex: llm.OutputPosition(4), Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"CURRENT"}`}}}},
		{name: "stale arguments", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"path":"CURRENT"}`}}}},
		{name: "conflicting output index", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "call", OutputIndex: llm.OutputPosition(4), Function: call.Function}}},
		{name: "negative output index", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "call", OutputIndex: llm.OutputPosition(-1), Function: call.Function}}},
		{name: "conflicting name", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "write", Arguments: call.Function.Arguments}}}},
		{name: "conflicting ID", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "other", Function: call.Function}}},
		{name: "malformed arguments", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{`}}}},
		{name: "nonobject arguments", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `[]`}}}},
		{name: "repeated native ID with display call", items: []llm.ContinuationItem{part, repeated}, calls: []llm.ToolCall{call}},
		{name: "repeated native ID without display call", items: []llm.ContinuationItem{part, repeated}},
		{name: "ambiguous anonymous native calls", items: []llm.ContinuationItem{anonymous, repeatedAnonymous}},
		{name: "repeated display ID", items: []llm.ContinuationItem{part}, calls: []llm.ToolCall{call, call.Clone()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertNativeRejectedBeforeDispatch(t, llm.Message{Role: llm.RoleAssistant, ToolCalls: test.calls, ContinuationItems: test.items})
		})
	}
}

func TestNativeContinuationAcceptsEquivalentCallBindings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		data      string
		arguments string
		itemID    string
		index     int
		callIndex *int
	}{
		{name: "JSON formatting key order and numeric equivalence", data: `{"functionCall":{"id":"call","name":"read","args":{"path":"a","extra":{"n":1,"flags":[true,null]}}},"thoughtSignature":"c2ln"}`, arguments: `{ "extra": { "flags": [true, null], "n": 1.0 }, "path": "a" }`, callIndex: llm.OutputPosition(0)},
		{name: "omitted display output index", data: `{"functionCall":{"id":"call","name":"read","args":{"path":"a"}},"thoughtSignature":"c2ln"}`, arguments: `{"path":"a"}`, index: 4},
		{name: "matching nonzero output index", data: `{"functionCall":{"id":"call","name":"read","args":{"path":"a"}},"thoughtSignature":"c2ln"}`, arguments: `{"path":"a"}`, index: 4, callIndex: llm.OutputPosition(4)},
		{name: "synthetic routing ID preserves absent native ID", data: `{"functionCall":{"name":"read","args":{"path":"a"}},"thoughtSignature":"c2ln"}`, arguments: `{"path":"a"}`, itemID: "call", callIndex: llm.OutputPosition(0)},
		{name: "empty arguments with nil native args", data: `{"functionCall":{"id":"call","name":"read"},"thoughtSignature":"c2ln"}`},
		{name: "null arguments", data: `{"functionCall":{"id":"call","name":"read"},"thoughtSignature":"c2ln"}`, arguments: `null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider, requests := nativePartsProvider(t, []string{`[{"text":"ok"}]`})
			part := nativePartItem(test.index, test.data)
			part.ID = test.itemID
			call := llm.ToolCall{ID: "call", OutputIndex: test.callIndex, Function: llm.ToolCallFunction{Name: "read", Arguments: test.arguments}}
			collectNativeAssistant(t, provider, []llm.Message{
				{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}, ContinuationItems: []llm.ContinuationItem{part}},
				{Role: llm.RoleTool, ToolCallID: call.ID, Content: "ok"},
			})
			request := <-requests
			var want map[string]any
			decodeNativeTestJSON(t, []byte(test.data), &want)
			if diff := cmp.Diff([]map[string]any{want}, nativeRequestParts(t, request, 0)); diff != "" {
				t.Fatalf("native replay (-want +got):\n%s", diff)
			}
			responses := nativeRequestParts(t, request, 1)
			if diff := cmp.Diff([]map[string]any{{"functionResponse": map[string]any{"id": "call", "name": "read", "response": map[string]any{"result": "ok"}}}}, responses); diff != "" {
				t.Fatalf("tool response binding (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNativeContinuationRejectsDistinctJSONArguments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		native  string
		display string
	}{
		{name: "adjacent large integers", native: `{"n":9007199254740992}`, display: `{"n":9007199254740993}`},
		{name: "adjacent large integers reversed", native: `{"n":9007199254740993}`, display: `{"n":9007199254740992}`},
		{name: "distinct precise fractions", native: `{"n":0.10000000000000001}`, display: `{"n":0.1}`},
		{name: "distinct precise fractions reversed", native: `{"n":0.1}`, display: `{"n":0.10000000000000001}`},
		{name: "nested null is not empty object", native: `{"n":null}`, display: `{"n":{}}`},
		{name: "missing field is not null", native: `{"n":null}`, display: `{}`},
		{name: "number is not string", native: `{"n":1}`, display: `{"n":"1"}`},
		{name: "array order matters", native: `{"n":[1,2]}`, display: `{"n":[2,1]}`},
		{name: "empty display cannot erase arguments", native: `{"n":1}`},
		{name: "null display cannot erase arguments", native: `{"n":1}`, display: `null`},
		{name: "trailing JSON", native: `{}`, display: `{} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			part := nativePartItem(0, `{"functionCall":{"id":"call","name":"read","args":`+test.native+`},"thoughtSignature":"c2ln"}`)
			call := llm.ToolCall{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: test.display}}
			assertNativeRejectedBeforeDispatch(t, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}, ContinuationItems: []llm.ContinuationItem{part}})
		})
	}
}

func TestNativeContinuationAcceptsParameterlessArguments(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, format := range []string{"generate_content.part.v1", "generate_content.part.v2"} {
				t.Run(format, func(t *testing.T) {
					t.Parallel()
					// The SDK omits nil and empty argument maps on the wire. Compatibility
					// treats only these root representations as the same parameterless call.
					for _, native := range []string{``, `,"args":null`, `,"args":{}`} {
						for _, display := range []string{``, `null`, `{}`} {
							t.Run(native+"/"+display, func(t *testing.T) {
								t.Parallel()
								provider, requests := nativePartsRouteProvider(t, route, []string{`[{"text":"ok"}]`})
								part := nativePartItem(0, `{"functionCall":{"id":"call","name":"read"`+native+`},"thoughtSignature":"c2ln"}`)
								part.Format = format
								part.ID = "call"
								call := llm.ToolCall{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: display}}
								collectNativeAssistant(t, provider, []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}, ContinuationItems: []llm.ContinuationItem{part}}})
								want := []map[string]any{{"functionCall": map[string]any{"id": "call", "name": "read"}, "thoughtSignature": "c2ln"}}
								if diff := cmp.Diff(want, nativeRequestParts(t, <-requests, 0)); diff != "" {
									t.Fatalf("parameterless native replay (-want +got):\n%s", diff)
								}
							})
						}
					}
				})
			}
		})
	}
}

func TestNativeContinuationAcceptsExactEquivalentNumbers(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, format := range []string{"generate_content.part.v1", "generate_content.part.v2"} {
				t.Run(format, func(t *testing.T) {
					t.Parallel()
					for _, test := range []struct {
						name    string
						native  string
						display string
					}{
						{name: "large integer", native: `9007199254740993`, display: `9007199254740993.0`},
						{name: "precise decimal", native: `0.10000000000000001`, display: `1.0000000000000001e-1`},
						{name: "large supported integer", native: `9007199254740992`, display: `9007199254740992.0`},
						{name: "ordinary decimal", native: `0.1`, display: `1e-1`},
						{name: "one decimal", native: `1`, display: `1.0`},
						{name: "one exponent", native: `1`, display: `1e0`},
						{name: "signed exponent", native: `1`, display: `1E+000`},
						{name: "trailing zero coefficient", native: `12.30`, display: `123e-1`},
						{name: "negative", native: `-1.25`, display: `-125e-2`},
						{name: "zero huge exponent", native: `-0e999999999999999999999999999999`, display: `0e-999999999999999999999999999999`},
						{name: "exponent", native: `100`, display: `1e2`},
						{name: "negative zero", native: `0`, display: `-0.0`},
					} {
						t.Run(test.name, func(t *testing.T) {
							t.Parallel()
							provider, requests := nativePartsRouteProvider(t, route, []string{`[{"text":"ok"}]`})
							metadata := ""
							// The pinned SDK supports open part metadata only on Gemini.
							if route.backend == googleRoutes[0].backend {
								metadata = `,"partMetadata":{"n":9007199254740993,"fraction":0.10000000000000001}`
							}
							part := nativePartItem(0, `{"functionCall":{"id":"call","name":"read","args":{"n":`+test.native+`}},"thoughtSignature":"c2ln"`+metadata+`}`)
							part.Format = format
							part.ID = "call"
							call := llm.ToolCall{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"n":` + test.display + `}`}}
							before := part.Clone()
							collectNativeAssistant(t, provider, []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}, ContinuationItems: []llm.ContinuationItem{part}}})
							// Binding comparison must not round distinct values or replace the
							// signed native numeric spelling with the display spelling.
							if diff := cmp.Diff(before, part); diff != "" {
								t.Fatalf("native input changed (-want +got):\n%s", diff)
							}
							parts := nativeRequestParts(t, <-requests, 0)
							var want map[string]any
							decodeNativeTestJSON(t, part.Data, &want)
							if diff := cmp.Diff([]map[string]any{want}, parts); diff != "" {
								t.Fatalf("exact native replay (-want +got):\n%s", diff)
							}
						})
					}
				})
			}
		})
	}
}

func TestProviderRejectsRepeatedStreamCallIDs(t *testing.T) {
	t.Parallel()
	for _, frames := range [][]string{
		{`[{"functionCall":{"id":"repeat","name":"read","args":{"path":"a"}}},{"functionCall":{"id":"repeat","name":"write","args":{"path":"b"}}}]`},
		{`[{"functionCall":{"id":"repeat","name":"read"}}]`, `[{"functionCall":{"id":"repeat","name":"read"}}]`},
		{`[{"functionCall":{"id":"call_read_1","name":"write"}},{"functionCall":{"name":"read"}}]`},
	} {
		t.Run(frames[0], func(t *testing.T) {
			t.Parallel()
			provider, requests := nativePartsProvider(t, frames)
			var calls []llm.ToolCall
			var items []llm.ContinuationItem
			failures := 0
			for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "start"}}}) {
				if err != nil {
					failures++
					continue
				}
				if failures != 0 {
					t.Fatal("yield after terminal error")
				}
				calls = append(calls, chunk.ToolCalls...)
				items = append(items, chunk.Clone().CompletedItems...)
			}
			<-requests
			if failures != 1 || len(calls) != 0 || len(items) != 1 {
				t.Fatalf("failures=%d calls=%#v items=%#v; want terminal rejection before overwrite", failures, calls, items)
			}
		})
	}
}

func TestToolResponsesUseNearestPriorNativeOrDisplayCall(t *testing.T) {
	t.Parallel()
	for _, nativeOnly := range []bool{false, true} {
		t.Run(strconv.FormatBool(nativeOnly), func(t *testing.T) {
			t.Parallel()
			provider, requests := nativePartsProvider(t, []string{`[{"text":"ok"}]`})
			var messages []llm.Message
			for _, name := range []string{"old", "current", "future"} {
				part := nativePartItem(0, `{"functionCall":{"id":"reused","name":"`+name+`"},"thoughtSignature":"c2ln"}`)
				assistant := llm.Message{Role: llm.RoleAssistant, ContinuationItems: []llm.ContinuationItem{part}}
				if !nativeOnly {
					assistant.ToolCalls = []llm.ToolCall{{ID: "reused", Function: llm.ToolCallFunction{Name: name}}}
				}
				messages = append(messages, assistant, llm.Message{Role: llm.RoleTool, ToolCallID: "reused", Content: "ok"})
			}
			collectNativeAssistant(t, provider, messages)
			request := <-requests
			for i, name := range []string{"old", "current", "future"} {
				parts := nativeRequestParts(t, request, i*2+1)
				response, ok := parts[0]["functionResponse"].(map[string]any)
				if !ok || response["name"] != name {
					t.Fatalf("response %d = %#v, want %s", i, parts, name)
				}
			}
		})
	}
}

func TestLegacyContinuationRejectsStaleOrAmbiguousBindings(t *testing.T) {
	t.Parallel()
	item := nativePartItem(0, `{"functionCall":{"name":"read","args":{"n":9007199254740993}},"thoughtSignature":"c2ln"}`)
	item.Format = "generate_content.part.v1"
	item.ID = "call"
	call := llm.ToolCall{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"n":9007199254740993}`}}
	for _, test := range []struct {
		name  string
		items []llm.ContinuationItem
		calls []llm.ToolCall
	}{
		{name: "stale arguments", items: []llm.ContinuationItem{item}, calls: []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"n":9007199254740992}`}}}},
		{name: "unbound legacy item", items: []llm.ContinuationItem{item}, calls: []llm.ToolCall{{ID: "different", Function: call.Function}}},
		{name: "missing display call", items: []llm.ContinuationItem{item}},
		{name: "malformed arguments", items: []llm.ContinuationItem{item}, calls: []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{`}}}},
		{name: "duplicate native bindings", items: []llm.ContinuationItem{item, item.Clone()}, calls: []llm.ToolCall{call}},
		{name: "duplicate display bindings", items: []llm.ContinuationItem{item}, calls: []llm.ToolCall{call, call.Clone()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertNativeRejectedBeforeDispatch(t, llm.Message{Role: llm.RoleAssistant, ContinuationItems: test.items, ToolCalls: test.calls})
		})
	}
}

// Unlike float64 rounding and underflow, overflow fails the pinned SDK's
// remarshal before the request-local exact-number body hook can execute.
func TestNativeContinuationRejectsUnencodableWireNumbers(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, number := range []string{"1e309", "1e1000000", "1e999999999999999999999999999999"} {
				for _, field := range []string{"matching call", "native only call", "part metadata", "function response"} {
					t.Run(number+"/"+field, func(t *testing.T) {
						t.Parallel()
						data := `{"functionCall":{"id":"call","name":"read","args":{"n":` + number + `}},"thoughtSignature":"c2ln"}`
						assistant := llm.Message{Role: llm.RoleAssistant}
						switch field {
						case "matching call":
							assistant.ToolCalls = []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"n":` + number + `}`}}}
						case "part metadata":
							data = `{"text":"native-canary","partMetadata":{"nested":[{"n":` + number + `}]},"thoughtSignature":"c2ln"}`
						case "function response":
							data = `{"functionResponse":{"name":"read","response":{"n":` + number + `}},"thoughtSignature":"c2ln"}`
						}
						assistant.ContinuationItems = []llm.ContinuationItem{nativePartItem(0, data)}
						provider, requests := nativePartsRouteProvider(t, route, []string{`[{"text":"unexpected"}]`})
						yields := 0
						for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{assistant}}) {
							yields++
							if err == nil || !strings.Contains(err.Error(), "continuation number cannot encode through SDK JSON") || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
								t.Fatalf("yield = %#v, %v", chunk, err)
							}
						}
						if yields != 1 {
							t.Fatalf("yields = %d", yields)
						}
						select {
						case <-requests:
							t.Fatal("unencodable native number dispatched HTTP")
						default:
						}
					})
				}
			}
		})
	}
}

func TestNativeContinuationHugeDisplayExponents(t *testing.T) {
	t.Parallel()
	for _, exponent := range []string{"1000000", "-1000000", "999999999999999999999999999999", "-999999999999999999999999999999"} {
		t.Run(exponent, func(t *testing.T) {
			t.Parallel()
			part := nativePartItem(0, `{"functionCall":{"id":"call","name":"read","args":{"n":1}},"thoughtSignature":"c2ln"}`)
			call := llm.ToolCall{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"n":1e` + exponent + `}`}}
			assertNativeRejectedBeforeDispatch(t, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}, ContinuationItems: []llm.ContinuationItem{part}})
		})
	}
}

func TestNativeContinuationCancelledHugeExponent(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			provider, requests := nativePartsRouteProvider(t, route, []string{`[{"text":"unexpected"}]`})
			ctx, cancel := context.WithCancelCause(t.Context())
			cause := errors.New("cancel validation")
			cancel(cause)
			part := nativePartItem(0, `{"functionCall":{"id":"call","name":"read","args":{"n":1}}}`)
			call := llm.ToolCall{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"n":1e` + strings.Repeat("9", 10000) + `}`}}
			yields := 0
			for chunk, err := range provider.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}, ContinuationItems: []llm.ContinuationItem{part}}}}) {
				yields++
				if !errors.Is(err, cause) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
					t.Fatalf("yield = %#v, %v", chunk, err)
				}
			}
			if yields != 1 {
				t.Fatalf("yields = %d", yields)
			}
			select {
			case <-requests:
				t.Fatal("cancelled validation dispatched HTTP")
			default:
			}
		})
	}
}

func TestNativeContinuationAcceptsWireNumbersOnBothRoutes(t *testing.T) {
	t.Parallel()
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, field := range []string{"matching call", "native only call", "part metadata", "function response"} {
				t.Run(field, func(t *testing.T) {
					t.Parallel()
					// The hook preserves even underflowing and otherwise lossy native numbers.
					values := `[0.1,1.0,1e0,-1.25,9007199254740992,1e-308,5e-324,1.7976931348623157e308,9007199254740993,0.10000000000000001,1e-400,1e-1000000,1e-999999999999999999999999999999]`
					data := `{"functionCall":{"id":"call","name":"read","args":{"values":` + values + `}},"thoughtSignature":"c2ln"}`
					assistant := llm.Message{Role: llm.RoleAssistant}
					switch field {
					case "matching call":
						assistant.ToolCalls = []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: `{"values":[1e-1,1,1.00,-125e-2,9007199254740992.0,1e-308,5e-324,1.7976931348623157e308,9007199254740993.0,1.0000000000000001e-1,10e-401,10e-1000001,10e-1000000000000000000000000000000]}`}}}
					case "part metadata":
						// The SDK does not support any partMetadata on Vertex AI.
						if route.name == "Vertex AI" {
							t.Skip("pinned SDK rejects partMetadata on Vertex AI")
						}
						data = `{"text":"native-canary","partMetadata":{"nested":[{"values":` + values + `}]},"thoughtSignature":"c2ln"}`
					case "function response":
						data = `{"functionResponse":{"name":"read","response":{"values":` + values + `}},"thoughtSignature":"c2ln"}`
					}
					part := nativePartItem(0, data)
					before := part.Clone()
					assistant.ContinuationItems = []llm.ContinuationItem{part}
					provider, requests := nativePartsRouteProvider(t, route, []string{`[{"text":"ok"}]`})
					collectNativeAssistant(t, provider, []llm.Message{assistant})
					var want map[string]any
					decodeNativeTestJSON(t, part.Data, &want)
					if diff := cmp.Diff([]map[string]any{want}, nativeRequestParts(t, <-requests, 0)); diff != "" {
						t.Fatalf("exact wire numbers (-want +got):\n%s", diff)
					}
					if diff := cmp.Diff(before, part); diff != "" {
						t.Fatalf("native input changed (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
}
