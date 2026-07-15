package openai

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestPlanRequest(t *testing.T) {
	t.Parallel()

	toolReq := llm.CompletionRequest{
		Stream: true,
		Tools: []llm.Tool{{
			Type: "function",
			Function: llm.ToolFunction{
				Name:       "read",
				Parameters: llm.SchemaFromMap(map[string]any{"type": "object"}),
			},
		}},
	}

	tests := []struct {
		name          string
		model         string
		req           llm.CompletionRequest
		wantChat      bool
		wantToken     TokenLimitField
		wantResponses bool
		wantReasoning bool
		wantErr       bool
	}{
		{
			name:      "ordinary chat no tools",
			model:     "gpt-4o-mini",
			wantChat:  true,
			wantToken: TokenLimitFields.TOKENLIMITMAXTOKENS,
		},
		{
			name:      "ordinary chat with tools",
			model:     "gpt-4o-mini",
			req:       toolReq,
			wantChat:  true,
			wantToken: TokenLimitFields.TOKENLIMITMAXTOKENS,
		},
		{
			name:      "o3 no tools uses max completion tokens",
			model:     "o3",
			wantChat:  true,
			wantToken: TokenLimitFields.TOKENLIMITMAXCOMPLETIONTOKENS,
		},
		{
			name:          "o3 mini tools uses responses",
			model:         "o3-mini",
			req:           toolReq,
			wantResponses: true,
		},
		{
			name:  "gpt 5 sol tools thinking uses responses reasoning",
			model: "gpt-5.6-sol",
			req: func() llm.CompletionRequest {
				r := toolReq
				r.Thinking = llm.ThinkingConfig{Enabled: true}
				return r
			}(),
			wantResponses: true,
			wantReasoning: true,
		},
		{
			name:          "case and space variation uses responses",
			model:         " GPT-5.6-SOL ",
			req:           toolReq,
			wantResponses: true,
		},
		{
			name:      "unknown compatible model is conservative chat",
			model:     "company-model-v7",
			req:       toolReq,
			wantChat:  true,
			wantToken: TokenLimitFields.TOKENLIMITMAXTOKENS,
		},
		{
			name:      "local style model is conservative chat",
			model:     "llama-3.3-local",
			req:       toolReq,
			wantChat:  true,
			wantToken: TokenLimitFields.TOKENLIMITMAXTOKENS,
		},
		{
			name:    "reasoning tools non stream rejected",
			model:   "gpt-5.6-sol",
			req:     llm.CompletionRequest{Tools: toolReq.Tools},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan, err := planRequest(tc.model, tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatal("planRequest error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("planRequest: %v", err)
			}

			if tc.wantChat {
				chat, ok := plan.(chatCompletionPlan)
				if !ok {
					t.Fatalf("plan = %T, want chatCompletionPlan", plan)
				}
				if chat.tokenLimit != tc.wantToken {
					t.Fatalf("tokenLimit = %s, want %s", chat.tokenLimit, tc.wantToken)
				}
			}
			if tc.wantResponses {
				resp, ok := plan.(responsesPlan)
				if !ok {
					t.Fatalf("plan = %T, want responsesPlan", plan)
				}
				if !resp.parallelToolCalls {
					t.Fatal("parallelToolCalls = false, want true")
				}
				if tc.wantReasoning {
					if resp.reasoning == nil || *resp.reasoning != ReasoningEfforts.REASONINGEFFORTMEDIUM {
						t.Fatalf("reasoning = %v, want medium", resp.reasoning)
					}
					if !resp.includeEncryptedReasoning {
						t.Fatal("includeEncryptedReasoning = false, want true")
					}
				}
			}
		})
	}
}

func TestOpenAIPlanningEnumWireValues(t *testing.T) {
	t.Parallel()

	checks := map[string]string{
		"endpoint chat":        EndpointKinds.ENDPOINTCHATCOMPLETIONS.String(),
		"endpoint responses":   EndpointKinds.ENDPOINTRESPONSES.String(),
		"token max_tokens":     TokenLimitFields.TOKENLIMITMAXTOKENS.String(),
		"token max_completion": TokenLimitFields.TOKENLIMITMAXCOMPLETIONTOKENS.String(),
		"token max_output":     TokenLimitFields.TOKENLIMITMAXOUTPUTTOKENS.String(),
		"reasoning low":        ReasoningEfforts.REASONINGEFFORTLOW.String(),
		"reasoning medium":     ReasoningEfforts.REASONINGEFFORTMEDIUM.String(),
		"reasoning high":       ReasoningEfforts.REASONINGEFFORTHIGH.String(),
		"reasoning xhigh":      ReasoningEfforts.REASONINGEFFORTXHIGH.String(),
		"reasoning max":        ReasoningEfforts.REASONINGEFFORTMAX.String(),
	}
	wants := map[string]string{
		"endpoint chat":        "chat_completions",
		"endpoint responses":   "responses",
		"token max_tokens":     "max_tokens",
		"token max_completion": "max_completion_tokens",
		"token max_output":     "max_output_tokens",
		"reasoning low":        "low",
		"reasoning medium":     "medium",
		"reasoning high":       "high",
		"reasoning xhigh":      "xhigh",
		"reasoning max":        "max",
	}
	for name, got := range checks {
		if got != wants[name] {
			t.Fatalf("%s = %q, want %q", name, got, wants[name])
		}
	}
}
