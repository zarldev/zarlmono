package litellm_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

func TestChatReservedServerToolIDsNeverReachClientExecution(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				call := `{"id":"srvtoolu_reserved","type":"function","function":{"name":"bash_code_execution","arguments":"{}"}}`
				if stream {
					call = strings.Replace(call, `{"id":`, `{"index":0,"id":`, 1)
					serveChatEvents(w, []string{chatEvent(`{"thinking_blocks":[{"type":"thinking","thinking":"protocol-private-canary","signature":"signed","index":0}]}`, ""), chatEvent(`{"tool_calls":[`+call+`]}`, "tool_calls"), "[DONE]"})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[{"type":"thinking","thinking":"protocol-private-canary","signature":"signed"}],"tool_calls":[`+call+`]}`, `{}`))
			}))
			t.Cleanup(server.Close)
			p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
			message, _, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
			if !errors.Is(err, litellm.ErrCompletion) || len(message.ToolCalls) != 0 || len(message.ContinuationItems) != 0 {
				t.Fatalf("server call surfaced: err=%v calls=%d items=%d", err, len(message.ToolCalls), len(message.ContinuationItems))
			}
			if strings.Contains(err.Error(), "protocol-private-canary") {
				t.Fatal("private diagnostic")
			}
		})
	}
}

func TestChatReservedServerToolHistoryFailsBeforeHTTP(t *testing.T) {
	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse(chatSignedMessage(), `{}`))
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
	first, _, err := collectChat(t, p, llm.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, signed := range []bool{false, true} {
		t.Run(strconv.FormatBool(signed), func(t *testing.T) {
			message := first.Clone()
			message.ToolCalls[0].ID = "srvtoolu_reserved"
			if signed {
				message.ContinuationItems[0].Data = bytesChatReplace(message.ContinuationItems[0].Data, `"real_call"`, `"srvtoolu_reserved"`)
			} else {
				message.ContinuationItems = nil
			}
			stream := p.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{message, {Role: llm.RoleTool, ToolCallID: "srvtoolu_reserved", Content: "fabricated result"}}})
			if count.Load() != 1 {
				t.Fatal("eager validation")
			}
			terminal := 0
			for chunk, err := range stream {
				if !errors.Is(err, litellm.ErrCompletion) || len(chunk.ToolCalls) != 0 || len(chunk.CompletedItems) != 0 {
					t.Fatalf("history accepted: %v", err)
				}
				terminal++
			}
			if count.Load() != 1 || terminal != 1 {
				t.Fatalf("requests=%d terminal=%d", count.Load(), terminal)
			}
		})
	}
}

func TestChatCacheBudgetFollowsTranslatedSystemOrder(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tt := range []struct {
			name, marker string
			systemTTL    openai.LiteLLMCacheTTL
			want         error
		}{
			{"short system before long conversation", "1h", openai.LiteLLMCacheTTLs.LITELLMCACHE5M, litellm.ErrCompletion},
			{"long system before short conversation", "5m", openai.LiteLLMCacheTTLs.LITELLMCACHE1H, nil},
		} {
			t.Run(fmt.Sprintf("%s/stream=%v", tt.name, stream), func(t *testing.T) {
				var count atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					count.Add(1)
					block := `{"type":"redacted_thinking","data":"opaque","cache_control":{"type":"ephemeral","ttl":"` + tt.marker + `"}}`
					if stream {
						block = strings.Replace(block, `"data":`, `"index":0,"data":`, 1)
						serveChatEvents(w, []string{chatEvent(`{"thinking_blocks":[`+block+`]}`, "stop"), "[DONE]"})
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[`+block+`]}`, `{}`))
				}))
				t.Cleanup(server.Close)
				// Replay authorization is deliberately independent of the shorter system
				// marker. A lifetime-authorization failure must not mask the order test.
				policy := openai.LiteLLMPromptCachePolicy{System: true, TTL: tt.systemTTL, AllowReplayMarkers: true, ReplayMaxTTL: openai.LiteLLMCacheTTLs.LITELLMCACHE1H}
				p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL), litellm.WithAnthropicPromptCache(policy))
				first, _, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
				if err != nil {
					t.Fatal(err)
				}
				if len(first.ContinuationItems) != 1 {
					t.Fatal("authorized marker missing")
				}
				// Hoisting must produce the same decision whether systems are early or late.
				for _, late := range []bool{false, true} {
					messages := []llm.Message{{Role: llm.RoleSystem, Content: "instructions"}, first.Clone()}
					if late {
						messages[0], messages[1] = messages[1], messages[0]
					}
					before := count.Load()
					_, _, err = collectChat(t, p, llm.CompletionRequest{Stream: stream, Messages: messages})
					if !errors.Is(err, tt.want) {
						t.Fatalf("late=%v error=%v want=%v", late, err, tt.want)
					}
					expected := before
					if tt.want == nil {
						expected++
					}
					if count.Load() != expected {
						t.Fatalf("late=%v requests=%d want=%d", late, count.Load(), expected)
					}
				}
			})
		}
	}
}

func TestChatOpaqueJSONUnicodeIsNotRepaired(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tt := range []struct {
			name, value string
			want        error
		}{
			{"high surrogate", `\ud800`, litellm.ErrCompletion},
			{"low surrogate", `\udfff`, litellm.ErrCompletion},
			{"unpaired before normal", `\ud800\u0061`, litellm.ErrCompletion},
			{"invalid UTF8", string([]byte{0xff}), litellm.ErrCompletion},
			{"paired supplementary", `\ud83d\ude00`, nil},
			{"literal escaped surrogate", `\\ud800`, nil},
			{"valid replacement rune", "\uFFFD", nil},
		} {
			for _, field := range []string{"signature", "data"} {
				t.Run(fmt.Sprintf("%s/%s/stream=%v", tt.name, field, stream), func(t *testing.T) {
					block := `{"type":"redacted_thinking","data":"` + tt.value + `"}`
					if field == "signature" {
						block = `{"type":"thinking","thinking":"private-unicode-canary","signature":"` + tt.value + `"}`
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						if stream {
							block := strings.Replace(block, `{"type":`, `{"index":0,"type":`, 1)
							serveChatEvents(w, []string{chatEvent(`{"thinking_blocks":[`+block+`]}`, "stop"), "[DONE]"})
							return
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[`+block+`]}`, `{}`))
					}))
					t.Cleanup(server.Close)
					p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
					msg, _, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
					if !errors.Is(err, tt.want) {
						t.Fatalf("error=%v want=%v", err, tt.want)
					}
					if tt.want != nil && (len(msg.ContinuationItems) != 0 || strings.Contains(err.Error(), "private-unicode-canary")) {
						t.Fatal("repaired or disclosed signed state")
					}
					if tt.want == nil && len(msg.ContinuationItems) != 1 {
						t.Fatal("valid Unicode missing envelope")
					}
				})
			}
		}
	}
}

func TestChatOwnedChoiceMetadataCannotSelectAlternateState(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, kind := range []string{"case choices", "case message", "case finish", "duplicate message", "duplicate finish"} {
			t.Run(fmt.Sprintf("%s/stream=%v", kind, stream), func(t *testing.T) {
				field := "message"
				message := `{"role":"assistant","thinking_blocks":[{"type":"redacted_thinking","data":"opaque"}]}`
				if stream {
					field = "delta"
					message = `{"thinking_blocks":[{"type":"redacted_thinking","data":"opaque","index":0}]}`
				}
				body := `{"choices":[{"index":0,"` + field + `":` + message + `,"finish_reason":"stop"}],"usage":{}}`
				switch kind {
				case "case choices":
					body = strings.Replace(body, `"choices":`, `"Choices":`, 1)
				case "case message":
					body = strings.Replace(body, `"`+field+`":`, `"`+strings.ToUpper(field)+`":`, 1)
				case "case finish":
					body = strings.Replace(body, `"finish_reason":`, `"Finish_Reason":`, 1)
				case "duplicate message":
					body = strings.Replace(body, `"`+field+`":`, `"`+field+`":{},"`+field+`":`, 1)
				case "duplicate finish":
					body = strings.Replace(body, `"finish_reason":`, `"finish_reason":"length","finish_reason":`, 1)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if stream {
						serveChatEvents(w, []string{body, "[DONE]"})
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				}))
				t.Cleanup(server.Close)
				p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
				msg, _, err := collectChat(t, p, llm.CompletionRequest{Stream: stream})
				if !errors.Is(err, litellm.ErrCompletion) || len(msg.ContinuationItems) != 0 {
					t.Fatalf("ambiguous choice accepted: err=%v items=%d", err, len(msg.ContinuationItems))
				}
			})
		}
	}
}

func TestChatStoredOpaqueUnicodeCannotBeRepaired(t *testing.T) {
	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatResponse(`{"role":"assistant","thinking_blocks":[{"type":"thinking","thinking":"private-stored-canary","signature":"\ufffd"},{"type":"redacted_thinking","data":"\ufffd"}]}`, `{}`))
	}))
	t.Cleanup(server.Close)
	p := litellm.NewProvider("key", litellm.WithBaseURL(server.URL))
	first, _, err := collectChat(t, p, llm.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"signature", "data"} {
		for _, bad := range []string{`\ud800`, `\udfff`, string([]byte{0xff})} {
			t.Run(fmt.Sprintf("%s/%x", field, bad), func(t *testing.T) {
				msg := first.Clone()
				raw := string(msg.ContinuationItems[0].Data)
				msg.ContinuationItems[0].Data = []byte(strings.Replace(raw, `"`+field+`":"`+"\uFFFD"+`"`, `"`+field+`":"`+bad+`"`, 1))
				if raw == string(msg.ContinuationItems[0].Data) {
					t.Fatal("fixture did not change")
				}
				_, _, err := collectChat(t, p, llm.CompletionRequest{Messages: []llm.Message{msg}})
				if !errors.Is(err, litellm.ErrCompletion) || count.Load() != 1 {
					t.Fatalf("stored state dispatched: err=%v requests=%d", err, count.Load())
				}
				if strings.Contains(err.Error(), "private-stored-canary") {
					t.Fatal("private diagnostic")
				}
			})
		}
	}
}
