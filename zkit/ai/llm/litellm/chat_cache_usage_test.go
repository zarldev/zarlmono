package litellm_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestChatExplicitAnthropicCachePolicy(t *testing.T) {
	cases := []struct {
		name                string
		policy              openai.LiteLLMPromptCachePolicy
		wantTTL             string
		explicit, automatic bool
	}{
		{name: "default off"},
		{name: "explicit default", policy: openai.LiteLLMPromptCachePolicy{System: true, Tools: true}, explicit: true},
		{name: "explicit 5m", policy: openai.LiteLLMPromptCachePolicy{System: true, Tools: true, TTL: openai.LiteLLMCacheTTLs.LITELLMCACHE5M}, wantTTL: "5m", explicit: true},
		{name: "explicit 1h", policy: openai.LiteLLMPromptCachePolicy{System: true, Tools: true, TTL: openai.LiteLLMCacheTTLs.LITELLMCACHE1H}, wantTTL: "1h", explicit: true},
		{name: "automatic", policy: openai.LiteLLMPromptCachePolicy{Automatic: true, TTL: openai.LiteLLMCacheTTLs.LITELLMCACHE5M}, wantTTL: "5m", automatic: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode: %v", err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","content":"ok"}`, `{}`))
			}))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithModel("claude-alias-is-not-policy"), litellm.WithAnthropicPromptCache(tt.policy))
			req := llm.CompletionRequest{Messages: []llm.Message{{Role: "system", Content: "instructions"}, {Role: llm.RoleUser, Content: "go"}}, Tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "first"}}, {Type: "function", Function: llm.ToolFunction{Name: "last"}}}}
			before, _ := json.Marshal(req.Messages)
			_, _, err := collectChat(t, p, req)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(req.Messages)
			if string(before) != string(after) {
				t.Fatal("borrowed request mutated")
			}
			body := <-requests
			system := body["messages"].([]any)[0].(map[string]any)
			tools := body["tools"].([]any)
			if tt.explicit {
				control := system["content"].([]any)[0].(map[string]any)["cache_control"].(map[string]any)
				assertChatCacheControl(t, control, tt.wantTTL)
				control = tools[1].(map[string]any)["cache_control"].(map[string]any)
				assertChatCacheControl(t, control, tt.wantTTL)
			} else if system["content"] != "instructions" {
				t.Fatalf("unexpected system marker=%v", system)
			}
			if _, ok := tools[0].(map[string]any)["cache_control"]; ok {
				t.Fatal("more than last tool marked")
			}
			if tt.automatic {
				assertChatCacheControl(t, body["cache_control"].(map[string]any), tt.wantTTL)
			} else if _, ok := body["cache_control"]; ok {
				t.Fatal("unexpected automatic cache")
			}
			if !tt.explicit {
				if _, ok := tools[1].(map[string]any)["cache_control"]; ok {
					t.Fatal("unexpected tool marker")
				}
			}
			for _, field := range []string{"cache_prompt", "chat_template_kwargs", "prompt_cache_retention", "thinking"} {
				if _, ok := body[field]; ok {
					t.Fatalf("unexpected %s", field)
				}
			}
		})
	}
}

func assertChatCacheControl(t *testing.T, control map[string]any, ttl string) {
	t.Helper()
	if control["type"] != "ephemeral" {
		t.Fatalf("control=%v", control)
	}
	if ttl == "" {
		if _, ok := control["ttl"]; ok {
			t.Fatal("default TTL should be omitted")
		}
	} else if control["ttl"] != ttl {
		t.Fatalf("TTL=%v want=%s", control["ttl"], ttl)
	}
}

func TestChatCachePolicyRejectionAndBudget(t *testing.T) {
	invalidTTL, _ := openai.ParseLiteLLMCacheTTL("invalid")
	cases := []struct {
		name    string
		policy  openai.LiteLLMPromptCachePolicy
		systems int
		want    error
	}{
		{"four", openai.LiteLLMPromptCachePolicy{System: true}, 4, nil},
		{"five", openai.LiteLLMPromptCachePolicy{System: true}, 5, litellm.ErrCompletion},
		{"automatic conflict", openai.LiteLLMPromptCachePolicy{System: true, Automatic: true}, 1, litellm.ErrCompletion},
		{"invalid TTL", openai.LiteLLMPromptCachePolicy{System: true, TTL: invalidTTL}, 1, litellm.ErrCompletion},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","content":"ok"}`, `{}`))
			}))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithAnthropicPromptCache(tt.policy))
			var req llm.CompletionRequest
			for range tt.systems {
				req.Messages = append(req.Messages, llm.Message{Role: "system", Content: "instructions"})
			}
			stream := p.Complete(t.Context(), req)
			if calls.Load() != 0 {
				t.Fatal("Complete not lazy")
			}
			var got error
			for _, err := range stream {
				if err != nil {
					got = err
				}
			}
			if !errors.Is(got, tt.want) {
				t.Fatalf("error=%v want=%v", got, tt.want)
			}
			if tt.want != nil && calls.Load() != 0 {
				t.Fatal("invalid policy dispatched")
			}
		})
	}
}

func TestChatUsagePresenceAliasesAndInclusivePrompt(t *testing.T) {
	cases := []struct {
		name, usage             string
		reported                bool
		read, write, five, hour llm.TokenQuantity
	}{
		{name: "missing", usage: "null"},
		{name: "reported empty", usage: `{}`, reported: true},
		{name: "null fields", usage: `{"prompt_tokens_details":{"cached_tokens":null,"cache_write_tokens":null}}`, reported: true},
		{name: "reported zeros", usage: `{"prompt_tokens":0,"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0,"cache_creation_token_details":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}`, reported: true, read: llm.TokenQuantity{Known: true}, write: llm.TokenQuantity{Known: true}, five: llm.TokenQuantity{Known: true}, hour: llm.TokenQuantity{Known: true}},
		{name: "canonical and aliases once", usage: `{"prompt_tokens":600,"completion_tokens":10,"total_tokens":610,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"prompt_tokens_details":{"cached_tokens":200,"cache_write_tokens":300,"cache_creation_tokens":300,"cache_creation_token_details":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":150}}}`, reported: true, read: llm.TokenQuantity{Tokens: 200, Known: true}, write: llm.TokenQuantity{Tokens: 300, Known: true}, five: llm.TokenQuantity{Tokens: 100, Known: true}, hour: llm.TokenQuantity{Tokens: 150, Known: true}},
		{name: "invalid TTL partition", usage: `{"prompt_tokens_details":{"cache_write_tokens":300,"cache_creation_token_details":{"ephemeral_5m_input_tokens":200,"ephemeral_1h_input_tokens":200}}}`, reported: true, write: llm.TokenQuantity{Tokens: 300, Known: true}},
		{name: "top aliases", usage: `{"prompt_tokens":600,"cache_read_input_tokens":200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":100}}`, reported: true, read: llm.TokenQuantity{Tokens: 200, Known: true}, write: llm.TokenQuantity{Tokens: 300, Known: true}, five: llm.TokenQuantity{Tokens: 100, Known: true}},
		{name: "independent read", usage: `{"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":null,"cache_creation_tokens":300}}`, reported: true, read: llm.TokenQuantity{Known: true}, write: llm.TokenQuantity{Tokens: 300, Known: true}},
		{name: "conflicting aliases unknown", usage: `{"cache_read_input_tokens":201,"cache_creation_input_tokens":301,"prompt_tokens_details":{"cached_tokens":200,"cache_write_tokens":300}}`, reported: true},
		{name: "invalid aliases unknown", usage: `{"cache_read_input_tokens":-1,"cache_creation_input_tokens":1.5}`, reported: true},
	}
	for _, stream := range []bool{false, true} {
		for _, tt := range cases {
			t.Run(fmt.Sprintf("%s/stream=%v", tt.name, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if stream {
						serveChatEvents(w, []string{chatEvent(`{"content":"ok"}`, "stop"), `{"choices":[],"usage":` + tt.usage + `}`, "[DONE]"})
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","content":"ok"}`, tt.usage))
				}))
				t.Cleanup(server.Close)
				p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
				_, chunk, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
				if err != nil {
					t.Fatal(err)
				}
				if chunk.UsageReported != tt.reported {
					t.Fatalf("reported=%v want=%v", chunk.UsageReported, tt.reported)
				}
				if !tt.reported {
					if chunk.PricingUsage != nil {
						t.Fatal("absent usage synthesized")
					}
					return
				}
				pricing := chunk.PricingUsage
				if pricing == nil || pricing.Input.CacheRead != tt.read || pricing.Input.CacheWrite != tt.write || pricing.Input.WritesByLifetime[0].Quantity != tt.five || pricing.Input.WritesByLifetime[1].Quantity != tt.hour {
					t.Fatalf("pricing=%+v", pricing)
				}
				if pricing.Input.PartitionKnown || pricing.Input.Other.Known || pricing.Output.Other.Known || !pricing.AdditionalChargesPossible {
					t.Fatalf("invented partition/coverage=%+v", pricing)
				}
				if pricing.Input.WritesByLifetime[0].Lifetime != 5*time.Minute || pricing.Input.WritesByLifetime[1].Lifetime != time.Hour {
					t.Fatal("TTL units")
				}
				if strings.Contains(tt.usage, `"prompt_tokens":600`) && (chunk.Usage.PromptTokens != 600 || pricing.Input.Total.Tokens != 600) {
					t.Fatalf("input counted twice: %+v", chunk)
				}
				if chunk.Usage.CacheCreationTokens != int(tt.write.Tokens) || chunk.Usage.CachedTokens != int(tt.read.Tokens) {
					t.Fatalf("legacy aliases=%+v", chunk.Usage)
				}
			})
		}
	}
}

func TestChatBodyCostMergesHeaderEvidenceWithoutSumming(t *testing.T) {
	cases := []struct {
		name, body, header, source string
		amount                     float64
		reported                   bool
	}{
		{name: "absent", body: "null"},
		{name: "zero", body: "0", source: "litellm-usage-cost", reported: true},
		{name: "body", body: "1.25", source: "litellm-usage-cost", amount: 1.25, reported: true},
		{name: "same header", body: "1.25", header: "1.25", source: "litellm-usage-cost", amount: 1.25, reported: true},
		{name: "conflict body selected", body: "1.25", header: "2.5", source: "litellm-usage-cost-conflicts-header", amount: 1.25, reported: true},
		{name: "negative", body: "-1"},
		{name: "underflow", body: "1e-999"},
		{name: "string", body: `"1.25"`},
	}
	for _, stream := range []bool{false, true} {
		for _, tt := range cases {
			t.Run(fmt.Sprintf("%s/stream=%v", tt.name, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Litellm-Include-Cost-In-Streaming-Usage") != "" {
						t.Error("v1.110 header requested")
					}
					w.Header().Set("X-Litellm-Call-Id", "call-observed")
					w.Header().Set("X-Litellm-Model-Id", "deployment-observed")
					if tt.header != "" {
						w.Header().Set("X-Litellm-Response-Cost", tt.header)
					}
					usage := `{"prompt_tokens":600,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":200,"cache_write_tokens":300},"cost":` + tt.body + `}`
					if stream {
						snapshot := `{"choices":[],"usage":` + usage + `}`
						serveChatEvents(w, []string{chatEvent(`{"content":"ok"}`, "stop"), snapshot, snapshot, "[DONE]"})
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","content":"ok"}`, usage))
				}))
				t.Cleanup(server.Close)
				p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithModel(alias))
				_, chunk, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
				if err != nil {
					t.Fatal(err)
				}
				observation := chunk.Execution
				if observation == nil || observation.GatewayCallID != "call-observed" || observation.GatewayDeployment != "deployment-observed" || observation.RequestedModel != llm.ModelID(alias) {
					t.Fatalf("lost header identity: %+v", observation)
				}
				cost := observation.ReportedCost
				if cost.Reported != tt.reported || cost.Amount != tt.amount || cost.Source != tt.source || cost.Currency != "" || cost.CoverageKnown {
					t.Fatalf("cost=%+v", cost)
				}
				if observation.PromptCacheReadTokens != (llm.TokenQuantity{Tokens: 200, Known: true}) || observation.PromptCacheWriteTokens != (llm.TokenQuantity{Tokens: 300, Known: true}) {
					t.Fatalf("cache execution=%+v", observation)
				}
			})
		}
	}
}

func TestChatCostSnapshotsAreNotSummedOrErased(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(strconv.FormatBool(conflict), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				events := []string{chatEvent(`{"content":"ok"}`, "stop"), `{"choices":[],"usage":{"cost":0}}`}
				if conflict {
					events = append(events, `{"choices":[],"usage":{"cost":1.25}}`)
				}
				events = append(events, `{"choices":[],"usage":{"prompt_tokens":10}}`, "[DONE]")
				serveChatEvents(w, events)
			}))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
			_, chunk, err := collectChat(t, p, llm.CompletionRequest{Stream: true})
			if err != nil {
				t.Fatal(err)
			}
			if chunk.Execution == nil || !chunk.Execution.ReportedCost.Reported {
				t.Fatal("body cost erased by absent field")
			}
			cost := chunk.Execution.ReportedCost
			if conflict {
				if cost.Amount != 1.25 || cost.Source != "litellm-usage-cost-conflicting-snapshots" {
					t.Fatalf("cost=%+v", cost)
				}
			} else if cost.Amount != 0 || cost.Source != "litellm-usage-cost" {
				t.Fatalf("cost=%+v", cost)
			}
		})
	}
}

func TestChatReplayCacheMarkersShareBudgetAndTTLOrder(t *testing.T) {
	cases := []struct {
		name, blocks     string
		policy           openai.LiteLLMPromptCachePolicy
		system, tool     bool
		responseRejected bool
	}{
		{"replayed four plus system", `[{"type":"redacted_thinking","data":"one","cache_control":{"type":"ephemeral"}},{"type":"redacted_thinking","data":"two","cache_control":{"type":"ephemeral"}},{"type":"redacted_thinking","data":"three","cache_control":{"type":"ephemeral"}},{"type":"redacted_thinking","data":"four","cache_control":{"type":"ephemeral"}}]`, openai.LiteLLMPromptCachePolicy{System: true, AllowReplayMarkers: true}, true, false, false},
		{"5m tool before 1h replay", `[{"type":"redacted_thinking","data":"one","cache_control":{"type":"ephemeral","ttl":"1h"}}]`, openai.LiteLLMPromptCachePolicy{Tools: true, AllowReplayMarkers: true, ReplayMaxTTL: openai.LiteLLMCacheTTLs.LITELLMCACHE5M}, false, true, true},
		{"automatic and explicit replay", `[{"type":"redacted_thinking","data":"one","cache_control":{"type":"ephemeral","ttl":"5m"}}]`, openai.LiteLLMPromptCachePolicy{Automatic: true, AllowReplayMarkers: true}, false, false, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","content":"answer","thinking_blocks":`+tt.blocks+`}`, `{}`))
			}))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithAnthropicPromptCache(tt.policy))
			first, _, err := collectChat(t, p, llm.CompletionRequest{})
			if tt.responseRejected {
				if !errors.Is(err, litellm.ErrCompletion) || len(first.ContinuationItems) != 0 {
					t.Fatalf("unauthorized response: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			req := llm.CompletionRequest{}
			if tt.system {
				req.Messages = append(req.Messages, llm.Message{Role: llm.RoleSystem, Content: "instructions"})
			}
			req.Messages = append(req.Messages, first)
			if tt.tool {
				req.Tools = []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "echo"}}}
			}
			_, _, err = collectChat(t, p, req)
			if !errors.Is(err, litellm.ErrCompletion) || calls.Load() != 1 {
				t.Fatalf("replay policy error=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestChatCacheMarkersInStreamingRequest(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		requests <- body
		serveChatEvents(w, chatSignedEvents())
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithAnthropicPromptCache(openai.LiteLLMPromptCachePolicy{System: true, Tools: true, TTL: openai.LiteLLMCacheTTLs.LITELLMCACHE1H}))
	_, _, err := collectChat(t, p, llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleSystem, Content: "instructions"}}, Tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "echo"}}}})
	if err != nil {
		t.Fatal(err)
	}
	body := <-requests
	system := body["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	assertChatCacheControl(t, system["cache_control"].(map[string]any), "1h")
	tool := body["tools"].([]any)[0].(map[string]any)
	assertChatCacheControl(t, tool["cache_control"].(map[string]any), "1h")
}

func TestChatResponseAndStoredMarkersRequireHostAuthorization(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tt := range []struct {
			name, ttl string
			policy    openai.LiteLLMPromptCachePolicy
			want      error
		}{
			{name: "default off", ttl: "5m", want: litellm.ErrCompletion},
			{name: "system is not replay permission", ttl: "5m", policy: openai.LiteLLMPromptCachePolicy{System: true}, want: litellm.ErrCompletion},
			{name: "tool is not replay permission", ttl: "5m", policy: openai.LiteLLMPromptCachePolicy{Tools: true}, want: litellm.ErrCompletion},
			{name: "automatic is not replay permission", ttl: "5m", policy: openai.LiteLLMPromptCachePolicy{Automatic: true}, want: litellm.ErrCompletion},
			{name: "explicit default permits 5m", ttl: "5m", policy: openai.LiteLLMPromptCachePolicy{AllowReplayMarkers: true}},
			{name: "explicit 1h permits 1h", ttl: "1h", policy: openai.LiteLLMPromptCachePolicy{AllowReplayMarkers: true, ReplayMaxTTL: openai.LiteLLMCacheTTLs.LITELLMCACHE1H}},
			{name: "explicit 5m rejects 1h", ttl: "1h", policy: openai.LiteLLMPromptCachePolicy{AllowReplayMarkers: true, ReplayMaxTTL: openai.LiteLLMCacheTTLs.LITELLMCACHE5M}, want: litellm.ErrCompletion},
		} {
			t.Run(fmt.Sprintf("%s/stream=%v", tt.name, stream), func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					if n > 1 {
						var body struct {
							Messages []map[string]json.RawMessage `json:"messages"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if len(body.Messages) != 1 || !strings.Contains(string(body.Messages[0]["thinking_blocks"]), `"ttl":"`+tt.ttl+`"`) {
							t.Error("replay marker changed or stripped")
						}
					}
					block := `{"type":"redacted_thinking","data":"cache-private-canary","cache_control":{"type":"ephemeral","ttl":"` + tt.ttl + `"}}`
					if stream {
						block = strings.Replace(block, `"type":"redacted_thinking"`, `"type":"redacted_thinking","index":0`, 1)
						serveChatEvents(w, []string{chatEvent(`{"thinking_blocks":[`+block+`]}`, "stop"), "[DONE]"})
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[`+block+`]}`, `{}`))
					}
				}))
				t.Cleanup(server.Close)
				p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithAnthropicPromptCache(tt.policy))
				first, _, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
				if !errors.Is(err, tt.want) {
					t.Fatalf("error=%v want=%v", err, tt.want)
				}
				if tt.want != nil {
					if len(first.ContinuationItems) != 0 || strings.Contains(err.Error(), "cache-private-canary") {
						t.Fatal("unauthorized response published private state")
					}
					return
				}
				if len(first.ContinuationItems) != 1 {
					t.Fatal("authorized response missing envelope")
				}
				_, _, err = collectChat(t, p, llm.CompletionRequest{Stream: stream, Messages: []llm.Message{first}})
				if err != nil || calls.Load() != 2 {
					t.Fatalf("authorized replay: %v calls=%d", err, calls.Load())
				}
			})
		}
	}
	// A marker inserted into otherwise valid, same-construction stored state must
	// not acquire authority merely because it came from a continuation envelope.
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[{"type":"redacted_thinking","data":"opaque"}]}`, `{}`))
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
	first, _, err := collectChat(t, p, llm.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	first.ContinuationItems[0].Data = []byte(strings.Replace(string(first.ContinuationItems[0].Data), `"data":"opaque"`, `"data":"opaque","cache_control":{"type":"ephemeral"}`, 1))
	_, _, err = collectChat(t, p, llm.CompletionRequest{Messages: []llm.Message{first}})
	if !errors.Is(err, litellm.ErrCompletion) || calls.Load() != 1 {
		t.Fatalf("stored marker dispatched: %v calls=%d", err, calls.Load())
	}
}

func TestChatAmbiguousMetadataIsTerminalBeforeInterpretation(t *testing.T) {
	cases := []struct{ name, root string }{
		{"duplicate cost", `"usage":{"cost":1.25,"cost":0}`},
		{"case cost", `"usage":{"cost":1.25,"Cost":0}`},
		{"duplicate usage", `"usage":{"cost":1.25},"usage":{"cost":0}`},
		{"case usage", `"usage":{"cost":1.25},"Usage":{"cost":0}`},
		{"Unicode case usage", `"usage":{"cost":1.25},"uſage":{"cost":0}`},
		{"Unicode case cost", `"usage":{"cost":1.25,"coſt":0}`},
		{"Unicode case cache read", `"usage":{"prompt_tokens_details":{"cached_tokens":100,"cached_toKens":0}}`},
		{"duplicate input", `"usage":{"prompt_tokens":100,"prompt_tokens":0}`},
		{"case output", `"usage":{"completion_tokens":100,"Completion_Tokens":0}`},
		{"duplicate cache read", `"usage":{"prompt_tokens_details":{"cached_tokens":100,"cached_tokens":0}}`},
		{"case cache write", `"usage":{"cache_creation_input_tokens":100,"Cache_Creation_Input_Tokens":0}`},
		{"duplicate details", `"usage":{"prompt_tokens_details":{"cached_tokens":100},"prompt_tokens_details":{"cached_tokens":0}}`},
		{"case details", `"usage":{"prompt_tokens_details":{},"Prompt_Tokens_Details":{}}`},
		{"duplicate TTL", `"usage":{"prompt_tokens_details":{"cache_creation_token_details":{"ephemeral_5m_input_tokens":100,"ephemeral_5m_input_tokens":0}}}`},
		{"case TTL", `"usage":{"cache_creation":{"ephemeral_1h_input_tokens":100,"Ephemeral_1h_Input_Tokens":0}}`},
		{"usage array", `"usage":[]`},
		{"details array", `"usage":{"prompt_tokens_details":[]}`},
		{"TTL scalar", `"usage":{"cache_creation":0}`},
	}
	for _, stream := range []bool{false, true} {
		for _, tt := range cases {
			t.Run(fmt.Sprintf("%s/stream=%v", tt.name, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if stream {
						serveChatEvents(w, []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"private-metadata-canary","signature":"s","index":0}],"content":"prefix"}`, "stop"), `{"choices":[],` + tt.root + `}`, "[DONE]"})
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"prefix","thinking_blocks":[{"type":"thinking","thinking":"private-metadata-canary","signature":"s"}]},"finish_reason":"stop"}],`+tt.root+`}`)
				}))
				t.Cleanup(server.Close)
				p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
				first, last, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
				if !errors.Is(err, litellm.ErrCompletion) || len(first.ContinuationItems) != 0 || last.UsageReported || last.PricingUsage != nil {
					t.Fatalf("ambiguous metadata consumed: err=%v usage=%+v items=%d", err, last.Usage, len(first.ContinuationItems))
				}
				if last.Execution != nil && last.Execution.ReportedCost.Reported {
					t.Fatal("ambiguous cost reported")
				}
				if strings.Contains(err.Error(), "private-metadata-canary") {
					t.Fatal("private diagnostic")
				}
				if stream && first.Content != "prefix" {
					t.Fatal("stream content lifecycle changed")
				}
			})
		}
	}
}

func TestChatReplayLifetimeAndResponseMarkerConstraints(t *testing.T) {
	for _, tt := range []struct {
		name, blocks string
		stored       bool
	}{
		{name: "stored longer lifetime", blocks: `[{"type":"redacted_thinking","data":"opaque","cache_control":{"type":"ephemeral","ttl":"5m"}}]`, stored: true},
		{name: "response lifetime order", blocks: `[{"type":"redacted_thinking","data":"one","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"redacted_thinking","data":"two","cache_control":{"type":"ephemeral","ttl":"1h"}}]`},
		{name: "five response markers", blocks: `[` + strings.TrimSuffix(strings.Repeat(`{"type":"redacted_thinking","data":"opaque","cache_control":{"type":"ephemeral"}},`, 5), ",") + `]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":`+tt.blocks+`}`, `{}`))
			}))
			t.Cleanup(server.Close)
			ttl := openai.LiteLLMCacheTTLs.LITELLMCACHE1H
			if tt.stored {
				ttl = openai.LiteLLMCacheTTLs.LITELLMCACHE5M
			}
			p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithAnthropicPromptCache(openai.LiteLLMPromptCachePolicy{AllowReplayMarkers: true, ReplayMaxTTL: ttl}))
			first, _, err := collectChat(t, p, llm.CompletionRequest{})
			if tt.stored {
				if err != nil {
					t.Fatal(err)
				}
				first.ContinuationItems[0].Data = []byte(strings.Replace(string(first.ContinuationItems[0].Data), `"ttl":"5m"`, `"ttl":"1h"`, 1))
				_, _, err = collectChat(t, p, llm.CompletionRequest{Messages: []llm.Message{first}})
			} else if len(first.ContinuationItems) != 0 {
				t.Fatal("invalid response marker constraints published")
			}
			if !errors.Is(err, litellm.ErrCompletion) || calls.Load() != 1 {
				t.Fatalf("marker constraints: %v calls=%d", err, calls.Load())
			}
		})
	}
}
