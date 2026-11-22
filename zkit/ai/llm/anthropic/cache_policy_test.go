package anthropic_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	anthropicprovider "github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
	"github.com/zarldev/zarlmono/zkit/options"
)

type cachePolicyTransport struct {
	calls int
	body  map[string]any
	raw   string
}

func (transport *cachePolicyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	transport.calls++
	defer req.Body.Close()
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read cache policy request: %w", err)
	}
	transport.raw = string(raw)
	if err := json.Unmarshal(raw, &transport.body); err != nil {
		return nil, fmt.Errorf("decode cache policy request: %w", err)
	}
	body := `{"id":"m","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{}}`
	contentType := "application/json"
	if transport.body["stream"] == true {
		contentType = "text/event-stream"
		body = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// cachePolicyBody exercises both transports through the public lazy API, with
// no network access. The request remains borrowed until iteration returns.
func cachePolicyBody(t *testing.T, req llm.CompletionRequest, wantErr error, opts ...options.Option[anthropicprovider.Provider]) map[string]any {
	t.Helper()
	transport := &cachePolicyTransport{}
	opts = append(opts, anthropicprovider.WithHTTPClient(&http.Client{Transport: transport}))
	provider := anthropicprovider.NewProvider("test-key", opts...)
	beforeMessages := llm.CloneMessages(req.Messages)
	before, err := json.Marshal(map[string]any{"messages": req.Messages, "tools": req.Tools, "options": req.Options})
	if err != nil {
		t.Fatal(err)
	}
	stream := provider.Complete(t.Context(), req)
	if transport.calls != 0 {
		t.Fatal("Complete dispatched before iteration")
	}
	errorsSeen := 0
	for chunk, completeErr := range stream {
		if completeErr == nil {
			if wantErr != nil {
				t.Fatal("invalid policy yielded a successful chunk")
			}
			continue
		}
		errorsSeen++
		if wantErr == nil || !errors.Is(completeErr, wantErr) {
			t.Fatalf("Complete error = %v, want %v", completeErr, wantErr)
		}
		if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
			t.Fatal("terminal error chunk is not zero")
		}
		if len(completeErr.Error()) > 256 {
			t.Fatal("unbounded policy diagnostic")
		}
		if strings.Contains(completeErr.Error(), "PRIVATE") {
			t.Fatalf("private input in error: %v", completeErr)
		}
	}
	wantCalls := 1
	if wantErr != nil {
		wantCalls = 0
		if errorsSeen != 1 {
			t.Fatalf("terminal errors = %d, want 1", errorsSeen)
		}
	}
	if transport.calls != wantCalls {
		t.Fatalf("HTTP calls = %d, want %d", transport.calls, wantCalls)
	}
	after, err := json.Marshal(map[string]any{"messages": req.Messages, "tools": req.Tools, "options": req.Options})
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || !reflect.DeepEqual(beforeMessages, req.Messages) {
		t.Fatal("borrowed request mutated")
	}
	return transport.body
}

func cacheNative(raw string) llm.ContinuationItem {
	return llm.ContinuationItem{Provider: "anthropic", Format: "content_block.v1", Data: []byte(raw)}
}

func cacheNativeMessage(blocks ...string) llm.Message {
	message := llm.Message{Role: llm.RoleAssistant}
	for _, block := range blocks {
		message.ContinuationItems = append(message.ContinuationItems, cacheNative(block))
	}
	return message
}

// cacheWireSites returns only recognized direct breakpoint objects, deliberately
// ignoring any nested cache_control names in opaque native/schema fields.
func cacheWireSites(body map[string]any) []map[string]any {
	var sites []map[string]any
	for _, key := range []string{"tools", "system"} {
		values, _ := body[key].([]any)
		for _, value := range values {
			sites = append(sites, value.(map[string]any))
		}
	}
	messages, _ := body["messages"].([]any)
	for _, message := range messages {
		for _, block := range message.(map[string]any)["content"].([]any) {
			sites = append(sites, block.(map[string]any))
		}
	}
	return sites
}

func cacheMarkerTTLs(body map[string]any) []string {
	var ttls []string
	for _, site := range cacheWireSites(body) {
		if control, ok := site["cache_control"].(map[string]any); ok {
			ttl, _ := control["ttl"].(string)
			ttls = append(ttls, ttl)
		}
	}
	return ttls
}

func cacheDefaultRequest() llm.CompletionRequest {
	return llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleSystem, Content: "system PRIVATE"}, {Role: llm.RoleUser, Content: "user PRIVATE"}},
		Tools:    []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "read"}}},
	}
}

func TestCachePolicyDefaultsAndPrecedence(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			tests := []struct {
				name string
				opts []options.Option[anthropicprovider.Provider]
				wire llm.ModelOptions
				ttls []string
			}{
				{name: "defaults", ttls: []string{"1h", "1h", ""}},
				{name: "constructor TTLs", opts: []options.Option[anthropicprovider.Provider]{anthropicprovider.WithCacheTTLs(anthropicprovider.CacheTTLs.CACHE5M, anthropicprovider.CacheTTLs.CACHE5M)}, ttls: []string{"", "", ""}},
				{name: "constructor native only", opts: []options.Option[anthropicprovider.Provider]{anthropicprovider.WithCacheStrategy(anthropicprovider.CacheStrategies.NATIVEONLY)}},
				{name: "request strategy preserves constructor TTLs", opts: []options.Option[anthropicprovider.Provider]{anthropicprovider.WithCacheStrategy(anthropicprovider.CacheStrategies.NATIVEONLY), anthropicprovider.WithCacheTTLs(anthropicprovider.CacheTTLs.CACHE1H, anthropicprovider.CacheTTLs.CACHE1H)}, wire: llm.ModelOptions{anthropicprovider.CacheStrategyOption: "three_region"}, ttls: []string{"1h", "1h", "1h"}},
				{name: "request strategy wins", opts: []options.Option[anthropicprovider.Provider]{anthropicprovider.WithCacheStrategy(anthropicprovider.CacheStrategies.NATIVEONLY)}, wire: llm.ModelOptions{anthropicprovider.CacheStrategyOption: "three_region"}, ttls: []string{"1h", "1h", ""}},
				{name: "request static only", opts: []options.Option[anthropicprovider.Provider]{anthropicprovider.WithCacheTTLs(anthropicprovider.CacheTTLs.CACHE1H, anthropicprovider.CacheTTLs.CACHE1H)}, wire: llm.ModelOptions{anthropicprovider.CacheStaticTTLOption: "5m", anthropicprovider.CacheRollingTTLOption: "5m"}, ttls: []string{"", "", ""}},
				{name: "request rolling only", wire: llm.ModelOptions{anthropicprovider.CacheRollingTTLOption: "1h"}, ttls: []string{"1h", "1h", "1h"}},
				{name: "request static preserves rolling", wire: llm.ModelOptions{anthropicprovider.CacheStaticTTLOption: "5m"}, ttls: []string{"", "", ""}},
				{name: "request native only", wire: llm.ModelOptions{anthropicprovider.CacheStrategyOption: "native_only"}},
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					req := cacheDefaultRequest()
					req.Stream, req.Options = stream, test.wire.Clone()
					body := cachePolicyBody(t, req, nil, test.opts...)
					if got := cacheMarkerTTLs(body); !reflect.DeepEqual(got, test.ttls) {
						t.Errorf("marker TTLs = %v, want %v", got, test.ttls)
					}
				})
			}
		})
	}
	var strategy anthropicprovider.CacheStrategy
	var ttl anthropicprovider.CacheTTL
	if strategy != anthropicprovider.CacheStrategies.THREEREGION || ttl != anthropicprovider.CacheTTLs.CACHE5M {
		t.Fatal("natural zeros changed")
	}
}

func TestCachePolicyInvalidRequestOptions(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, key := range []string{anthropicprovider.CacheStrategyOption, anthropicprovider.CacheStaticTTLOption, anthropicprovider.CacheRollingTTLOption} {
			for _, value := range []any{nil, "", "PRIVATE", " 5m", "THREE_REGION", 5, true, []any{"5m"}, map[string]any{"ttl": "5m"}, anthropicprovider.CacheTTLs.CACHE5M} {
				t.Run(fmt.Sprintf("%v/%s/%T/%v", stream, key, value, value), func(t *testing.T) {
					req := cacheDefaultRequest()
					req.Stream = stream
					req.Options = llm.ModelOptions{key: value}
					cachePolicyBody(t, req, anthropicprovider.ErrInvalidCachePolicy)
				})
			}
		}
	}
}

func TestCachePolicyBackwardSelection(t *testing.T) {
	thinking := `{"type":"thinking","thinking":"PRIVATE","signature":"PRIVATE-signature"}`
	redacted := `{"type":"redacted_thinking","data":"PRIVATE-data"}`
	empty := `{"type":"text","text":""}`
	for _, stream := range []bool{false, true} {
		tests := []struct {
			name     string
			messages []llm.Message
			kind     string
		}{
			{"text before tails", []llm.Message{cacheNativeMessage(`{"type":"text","text":"keep"}`, thinking, redacted, empty)}, "text"},
			{"cross message", []llm.Message{{Role: llm.RoleUser, Content: "prior"}, cacheNativeMessage(thinking, redacted, empty)}, "text"},
			{"tool use", []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call", Function: llm.ToolCallFunction{Name: "read", Arguments: "{}"}}}}, cacheNativeMessage(thinking)}, "tool_use"},
			{"coalesced tool results", []llm.Message{{Role: llm.RoleTool, ToolCallID: "a", Content: "one"}, {Role: llm.RoleTool, ToolCallID: "b", Content: "two"}, cacheNativeMessage(thinking)}, "tool_result"},
			{"image", []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: "https://example.com/image.png"}}}}, cacheNativeMessage(thinking)}, "image"},
			{"no eligible", []llm.Message{cacheNativeMessage(thinking, redacted, empty)}, ""},
			{"no messages", nil, ""},
		}
		for _, test := range tests {
			t.Run(fmt.Sprintf("%v/%s", stream, test.name), func(t *testing.T) {
				req := llm.CompletionRequest{Stream: stream, Messages: test.messages}
				body := cachePolicyBody(t, req, nil)
				marked := 0
				for _, site := range cacheWireSites(body) {
					if _, ok := site["cache_control"]; !ok {
						continue
					}
					marked++
					if site["type"] != test.kind {
						t.Fatalf("marked type = %v, want %s", site["type"], test.kind)
					}
					if test.name == "coalesced tool results" && site["tool_use_id"] != "b" {
						t.Fatal("did not select last coalesced tool result")
					}
				}
				want := 1
				if test.kind == "" {
					want = 0
				}
				if marked != want {
					t.Errorf("markers = %d, want %d", marked, want)
				}
			})
		}
	}
}

func TestCachePolicyNativeSlotsAndOrdering(t *testing.T) {
	short := `{"type":"text","text":"PRIVATE","cache_control":{"type":"ephemeral"}}`
	explicitShort := `{"type":"text","text":"PRIVATE","cache_control":{"type":"ephemeral","ttl":"5m"}}`
	long := `{"type":"text","text":"PRIVATE","cache_control":{"type":"ephemeral","ttl":"1h"}}`
	unmarked := `{"type":"text","text":"PRIVATE"}`
	for _, stream := range []bool{false, true} {
		tests := []struct {
			name   string
			blocks []string
			native bool
			static bool
			wire   llm.ModelOptions
			ttls   []string
			err    error
		}{
			{name: "native zero", blocks: []string{unmarked}, native: true},
			{name: "native four", blocks: []string{long, short, explicitShort, short}, native: true, ttls: []string{"1h", "", "5m", ""}},
			{name: "native five", blocks: []string{short, short, short, short, short}, native: true, err: anthropicprovider.ErrInvalidCachePolicy},
			{name: "combined four", blocks: []string{short, short, short, unmarked}, ttls: []string{"", "", "", ""}},
			{name: "combined five", blocks: []string{short, short, short, short, unmarked}, err: anthropicprovider.ErrInvalidCachePolicy},
			{name: "selected native no extra earlier", blocks: []string{short, short, short, unmarked, short}, ttls: []string{"", "", "", ""}},
			{name: "static combined four", static: true, blocks: []string{short, short}, ttls: []string{"1h", "1h", "", ""}},
			{name: "static combined five", static: true, blocks: []string{short, short, short}, err: anthropicprovider.ErrInvalidCachePolicy},
			{name: "native invalid order", native: true, blocks: []string{short, long}, err: anthropicprovider.ErrInvalidCachePolicy},
			{name: "generated rolling order conflict", blocks: []string{short, unmarked}, wire: llm.ModelOptions{anthropicprovider.CacheRollingTTLOption: "1h"}, err: anthropicprovider.ErrInvalidCachePolicy},
			{name: "generated static order conflict", static: true, blocks: []string{long}, wire: llm.ModelOptions{anthropicprovider.CacheStaticTTLOption: "5m"}, err: anthropicprovider.ErrInvalidCachePolicy},
			{name: "selected long wins", blocks: []string{long}, ttls: []string{"1h"}},
			{name: "selected explicit short wins", blocks: []string{explicitShort}, wire: llm.ModelOptions{anthropicprovider.CacheRollingTTLOption: "1h"}, ttls: []string{"5m"}},
			{name: "all long valid", static: true, blocks: []string{long, unmarked}, wire: llm.ModelOptions{anthropicprovider.CacheRollingTTLOption: "1h"}, ttls: []string{"1h", "1h", "1h", "1h"}},
		}
		for _, test := range tests {
			t.Run(fmt.Sprintf("%v/%s", stream, test.name), func(t *testing.T) {
				req := llm.CompletionRequest{Stream: stream, Messages: []llm.Message{cacheNativeMessage(test.blocks...)}, Options: test.wire.Clone()}
				if test.static {
					defaults := cacheDefaultRequest()
					req.Tools = defaults.Tools
					req.Messages = append(defaults.Messages[:1:1], req.Messages...)
				}
				if test.native {
					if req.Options == nil {
						req.Options = llm.ModelOptions{}
					}
					req.Options[anthropicprovider.CacheStrategyOption] = "native_only"
				}
				body := cachePolicyBody(t, req, test.err)
				if test.err == nil {
					if got := cacheMarkerTTLs(body); !reflect.DeepEqual(got, test.ttls) {
						t.Errorf("marker TTLs = %#v, want %#v", got, test.ttls)
					}
				}
			})
		}
	}
}

func TestCachePolicyRejectsMalformedNativeControls(t *testing.T) {
	controls := []string{
		`null`, `false`, `[]`, `"PRIVATE"`, `{}`, `{"type":null}`, `{"type":"PRIVATE"}`,
		`{"type":"ephemeral","ttl":null}`, `{"type":"ephemeral","ttl":""}`,
		`{"type":"ephemeral","ttl":"PRIVATE"}`, `{"type":"ephemeral","ttl":5}`,
		`{"type":"ephemeral","ttl":"5M"}`, `{"type":"ephemeral","ttl":" 5m"}`,
		`{"type":"ephemeral","PRIVATE":"PRIVATE"}`,
		`{"type":"ephemeral","type":"ephemeral"}`,
		`{"type":"ephemeral","ttl":"1h","ttl":"5m"}`,
		`{"type":"ephemeral","ttl":"5m","\u0074tl":"1h"}`,
	}
	var blocks []string
	for _, control := range controls {
		blocks = append(blocks, `{"type":"text","text":"PRIVATE","cache_control":`+control+`}`)
	}
	blocks = append(blocks,
		`{"type":"text","type":"thinking","text":"PRIVATE","thinking":"PRIVATE","signature":"PRIVATE"}`,
		`{"type":"thinking","type":"text","text":"PRIVATE"}`,
		`{"type":"text","\u0074ype":"text","text":"PRIVATE"}`,
		`{"type":"text","text":"PRIVATE","text":"PRIVATE"}`,
		`{"type":"text","text":"PRIVATE","cache_control":{"type":"ephemeral"},"cache_control":{"type":"ephemeral"}}`,
		`{"type":"thinking","thinking":"PRIVATE","signature":"PRIVATE","cache_control":{"type":"ephemeral"}}`,
		`{"type":"redacted_thinking","data":"PRIVATE","cache_control":{"type":"ephemeral"}}`,
		`{"type":"text","text":"","cache_control":{"type":"ephemeral"}}`,
		`{"type":"text","text":null,"cache_control":{"type":"ephemeral"}}`,
		`{"type":"text","text":5,"cache_control":{"type":"ephemeral"}}`,
	)
	for _, stream := range []bool{false, true} {
		for i, block := range blocks {
			for _, strategy := range []string{"three_region", "native_only"} {
				t.Run(fmt.Sprintf("%v/%s/%d", stream, strategy, i), func(t *testing.T) {
					req := llm.CompletionRequest{Stream: stream, Messages: []llm.Message{cacheNativeMessage(block)}, Options: llm.ModelOptions{anthropicprovider.CacheStrategyOption: strategy}}
					cachePolicyBody(t, req, anthropicprovider.ErrInvalidCachePolicy)
				})
			}
		}
	}
}

func TestCachePolicyPreservesNativeWireFields(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, marked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/marked=%v", stream, marked), func(t *testing.T) {
				raw := `{"type":"text","text":"PRIVATE","citations":[{"type":"PRIVATE","cache_control":null}],"opaque":{"cache_control":{"type":"PRIVATE"},"text":"one","text":"two"},"unknown":[true,null,42]`
				if marked {
					raw += `,"cache_control":{"type":"ephemeral","ttl":"1h"}`
				}
				raw += `}`
				thinking := `{"type":"thinking","thinking":"PRIVATE","signature":"PRIVATE-signature","unknown":{"cache_control":null}}`
				redacted := `{"type":"redacted_thinking","data":"PRIVATE-redacted","unknown":true}`
				req := llm.CompletionRequest{Stream: stream, Messages: []llm.Message{cacheNativeMessage(raw, thinking, redacted)}}
				body := cachePolicyBody(t, req, nil)
				sites := cacheWireSites(body)
				for i, original := range []string{raw, thinking, redacted} {
					var want map[string]any
					if err := json.Unmarshal([]byte(original), &want); err != nil {
						t.Fatal(err)
					}
					if i == 0 && !marked {
						want["cache_control"] = map[string]any{"type": "ephemeral"}
					}
					if !reflect.DeepEqual(sites[i], want) {
						t.Errorf("native site %d changed: got %#v, want %#v", i, sites[i], want)
					}
				}
				if len(cacheMarkerTTLs(body)) != 1 {
					t.Fatal("extra marker on earlier or signed block")
				}
			})
		}
	}
}

func TestCachePolicyLongHistoryPlacement(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, count := range []int{19, 20, 21, 200} {
			t.Run(fmt.Sprintf("%v/%d", stream, count), func(t *testing.T) {
				req := llm.CompletionRequest{Stream: stream}
				req.Messages = append(req.Messages, cacheNativeMessage(`{"type":"text","text":"old write","cache_control":{"type":"ephemeral"}}`))
				for i := 1; i < count; i++ {
					role := llm.RoleUser
					if i%2 != 0 {
						role = llm.RoleAssistant
					}
					req.Messages = append(req.Messages, llm.Message{Role: role, Content: fmt.Sprintf("turn-%d", i)})
				}
				body := cachePolicyBody(t, req, nil)
				sites := cacheWireSites(body)
				if len(sites) != count || len(cacheMarkerTTLs(body)) != 2 {
					t.Fatal("history altered or heuristic markers introduced")
				}
				if _, ok := sites[0]["cache_control"]; !ok {
					t.Fatal("old native marker was not preserved")
				}
				if _, ok := sites[len(sites)-1]["cache_control"]; !ok {
					t.Fatal("last eligible block was not selected")
				}
			})
		}
	}
}

func TestCachePolicyAbsentRegions(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tools := range []bool{false, true} {
			for _, system := range []bool{false, true} {
				for _, message := range []bool{false, true} {
					t.Run(fmt.Sprintf("%v/tools=%v/system=%v/message=%v", stream, tools, system, message), func(t *testing.T) {
						req := cacheDefaultRequest()
						req.Stream = stream
						var want []string
						if tools {
							want = append(want, "1h")
						} else {
							req.Tools = nil
						}
						messages := req.Messages
						req.Messages = nil
						if system {
							req.Messages = append(req.Messages, messages[0])
							want = append(want, "1h")
						}
						if message {
							req.Messages = append(req.Messages, messages[1])
							want = append(want, "")
						}
						body := cachePolicyBody(t, req, nil)
						if got := cacheMarkerTTLs(body); !reflect.DeepEqual(got, want) {
							t.Errorf("marker TTLs = %#v, want %#v", got, want)
						}
					})
				}
			}
		}
	}
}

func TestCachePolicyLazyBorrowingAndProviderIsolation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			transport := &cachePolicyTransport{}
			provider := anthropicprovider.NewProvider("test-key", anthropicprovider.WithHTTPClient(&http.Client{Transport: transport}))
			req := cacheDefaultRequest()
			req.Stream = stream
			req.Options = llm.ModelOptions{anthropicprovider.CacheStrategyOption: "PRIVATE-invalid"}
			completion := provider.Complete(t.Context(), req)
			// Complete must neither parse options nor snapshot borrowed request
			// data. The caller's valid change before iteration is observed.
			req.Options[anthropicprovider.CacheStrategyOption] = "native_only"
			for _, err := range completion {
				if err != nil {
					t.Fatalf("policy was parsed before iteration: %v", err)
				}
			}
			if transport.calls != 1 || len(cacheMarkerTTLs(transport.body)) != 0 {
				t.Fatal("lazy native-only override was not observed")
			}
			req.Options = nil
			for _, err := range provider.Complete(t.Context(), req) {
				if err != nil {
					t.Fatal(err)
				}
			}
			if transport.calls != 2 || !reflect.DeepEqual(cacheMarkerTTLs(transport.body), []string{"1h", "1h", ""}) {
				t.Fatal("request override mutated provider defaults")
			}
		})
	}
}

func TestCachePolicyRawAdditionPreservesOpaqueBytes(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, strategy := range []string{"three_region", "native_only"} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, strategy), func(t *testing.T) {
				transport := &cachePolicyTransport{}
				provider := anthropicprovider.NewProvider("test-key", anthropicprovider.WithHTTPClient(&http.Client{Transport: transport}))
				raw := `{"type":"text","text":"PRIVATE","opaque":{"type":"text","Type":"thinking","text":"one","text":"two","Text":"","cache_control":null,"Cache_Control":null},"unknown_number":9007199254740993,"Type_metadata":"unrelated"}`
				req := llm.CompletionRequest{Stream: stream, Messages: []llm.Message{cacheNativeMessage(raw)}, Options: llm.ModelOptions{anthropicprovider.CacheStrategyOption: strategy}}
				for _, err := range provider.Complete(t.Context(), req) {
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, unchanged := range []string{`"opaque":{"type":"text","Type":"thinking","text":"one","text":"two","Text":"","cache_control":null,"Cache_Control":null}`, `"unknown_number":9007199254740993`, `"Type_metadata":"unrelated"`} {
					if !strings.Contains(transport.raw, unchanged) {
						t.Fatal("opaque native JSON was normalized or lost")
					}
				}
				wantMarkers := 0
				if strategy == "three_region" {
					wantMarkers = 1
				}
				if len(cacheMarkerTTLs(transport.body)) != wantMarkers || string(req.Messages[0].ContinuationItems[0].Data) != raw {
					t.Fatal("native input mutated or unexpected rolling marker count")
				}
			})
		}
	}
}

func TestCachePolicyRejectsStructuralAliases(t *testing.T) {
	blocks := []struct {
		name string
		raw  string
	}{
		{"thinking shadows visible answer", `{"type":"thinking","Type":"text","thinking":"PRIVATE-signed-thought","signature":"PRIVATE-signature","text":"shadow"}`},
		{"text shadows thinking", `{"type":"text","Type":"thinking","text":"visible","thinking":"PRIVATE-synthetic-thought","signature":"PRIVATE-signature","cache_control":{"type":"ephemeral"}}`},
	}
	for _, field := range []struct {
		key, alias, other, canonical, shadow string
	}{
		{"type", "Type", `"text":"PRIVATE-visible","thinking":"PRIVATE-thought","signature":"PRIVATE-signature"`, `"text"`, `"thinking"`},
		{"text", "Text", `"type":"text"`, `"PRIVATE-visible"`, `""`},
		{"cache_control", "Cache_Control", `"type":"text","text":"PRIVATE-visible"`, `{"type":"ephemeral"}`, `null`},
	} {
		for _, alias := range []string{field.alias, strings.ToUpper(field.alias)} {
			for _, key := range []string{alias, `\u` + fmt.Sprintf("%04x", alias[0]) + alias[1:]} {
				canonical := `"` + field.key + `":` + field.canonical
				shadow := `"` + key + `":` + field.shadow
				for i, members := range []string{canonical + "," + shadow, shadow + "," + canonical, shadow, `"` + key + `":` + field.canonical} {
					blocks = append(blocks, struct {
						name string
						raw  string
					}{fmt.Sprintf("%s/%s/order=%d", field.key, key, i), "{" + field.other + "," + members + "}"})
				}
			}
		}
	}
	for _, block := range blocks {
		for _, stream := range []bool{false, true} {
			for _, strategy := range []string{"three_region", "native_only"} {
				for _, role := range []string{llm.RoleAssistant, llm.RoleUser} {
					t.Run(fmt.Sprintf("%s/stream=%v/%s/%s", block.name, stream, strategy, role), func(t *testing.T) {
						message := cacheNativeMessage(block.raw)
						message.Role = role
						message.Content = "VISIBLE ANSWER MUST NOT DISAPPEAR"
						req := llm.CompletionRequest{Stream: stream, Messages: []llm.Message{message}, Options: llm.ModelOptions{anthropicprovider.CacheStrategyOption: strategy}}
						cachePolicyBody(t, req, anthropicprovider.ErrInvalidCachePolicy)
					})
				}
			}
		}
	}
}
