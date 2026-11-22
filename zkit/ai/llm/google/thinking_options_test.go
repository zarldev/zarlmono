package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
	"github.com/zarldev/zarlmono/zkit/options"
)

type thinkingControlCase struct {
	model      string
	options    llm.ModelOptions
	thinking   llm.ThinkingConfig
	wantBudget *int
	wantLevel  string
	wantErr    error
}

func TestThinkingBudgetBoundaries(t *testing.T) {
	for _, policy := range []struct {
		model            string
		minimum, maximum int
		off              bool
	}{
		{"gemini-2.5-flash", 1, 24576, true},
		{"gemini-2.5-pro", 128, 32768, false},
		{"gemini-2.5-flash-lite", 512, 24576, true},
	} {
		for _, prefix := range []string{"", "models/"} {
			for _, tokens := range []int{math.MinInt, -2, -1, 0, 1, policy.minimum - 1, policy.minimum, policy.maximum, policy.maximum + 1, math.MaxInt32, math.MaxInt} {
				for _, raw := range []bool{false, true} {
					for _, enabled := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s%s/%d/raw=%t/enabled=%t", prefix, policy.model, tokens, raw, enabled), func(t *testing.T) {
							tc := thinkingControlCase{model: prefix + policy.model, thinking: llm.ThinkingConfig{Enabled: enabled, BudgetTokens: tokens}}
							if raw {
								tc.options = llm.ModelOptions{"thinking_budget": tokens}
								tc.thinking.BudgetTokens = 0
							}
							switch {
							case tokens == 0 && !raw: // Typed zero is omission, even on Pro.
							case tokens == -1 || tokens == 0 && policy.off || tokens >= policy.minimum && tokens <= policy.maximum:
								tc.wantBudget = new(tokens)
							default:
								tc.wantErr = google.ErrInvalidThinkingConfig
							}
							runThinkingControlModes(t, tc)
						})
					}
				}
			}
		}
	}
}

func TestThinkingLevelModelMatrix(t *testing.T) {
	for _, policy := range []struct {
		model, levels string
	}{
		{"gemini-2.5-flash", ""},
		{"gemini-2.5-pro", ""},
		{"gemini-2.5-flash-lite", ""},
		{"", "LOW MEDIUM HIGH"}, // Stable default is the same exact policy, not a budget model.
		{"gemini-3.8-flash", "LOW MEDIUM HIGH"},
		{"gemini-3.6-flash", "MINIMAL LOW MEDIUM HIGH"},
		{"gemini-3.5-flash", "MINIMAL LOW MEDIUM HIGH"},
		{"gemini-3.5-flash-lite", "MINIMAL LOW MEDIUM HIGH"},
		{"gemini-4-argon", "LOW MEDIUM HIGH"},
		{"gemini-4-carbon", "LOW MEDIUM HIGH"},
		{"gemini-4-hydrogen", "LOW MEDIUM HIGH XHIGH"},
		{"gemini-3-pro-preview", "LOW HIGH"},
		{"gemini-3.1-pro-preview", "LOW MEDIUM HIGH"},
		{"gemini-3-flash-preview", "MINIMAL LOW MEDIUM HIGH"},
		{"gemini-3.1-flash-lite-preview", "MINIMAL LOW MEDIUM HIGH"},
		{"gemini-3.1-flash-lite", "MINIMAL LOW MEDIUM HIGH"},
	} {
		for _, prefix := range []string{"", "models/"} {
			if policy.model == "" && prefix != "" {
				continue
			}
			for _, level := range []string{"", "DEFAULT", "MINIMAL", "LOW", "MEDIUM", "HIGH", "XHIGH"} {
				t.Run(prefix+policy.model+"/"+level, func(t *testing.T) {
					tc := thinkingControlCase{
						model: prefix + policy.model, options: llm.ModelOptions{"thinking_level": " \t" + strings.ToLower(level) + "\n"},
						thinking: llm.ThinkingConfig{Enabled: true}, wantErr: google.ErrInvalidThinkingConfig,
					}
					if slices.Contains(strings.Fields(policy.levels), level) {
						tc.wantErr, tc.wantLevel = nil, level
					}
					runThinkingControlModes(t, tc)
				})
			}
			// No budgets, including off and dynamic, are legal on level-only models.
			if policy.levels != "" {
				for _, tokens := range []int{-1, 0, 128} {
					for _, raw := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s%s/budget=%d/raw=%t", prefix, policy.model, tokens, raw), func(t *testing.T) {
							tc := thinkingControlCase{model: prefix + policy.model, thinking: llm.ThinkingConfig{BudgetTokens: tokens}}
							if raw {
								tc.options = llm.ModelOptions{"thinking_budget": tokens}
							}
							if raw || tokens != 0 {
								tc.wantErr = google.ErrInvalidThinkingConfig
							}
							runThinkingControlModes(t, tc)
						})
					}
				}
			}
		}
	}
}

func TestThinkingRawTypesPrecedenceAndConflicts(t *testing.T) {
	for _, value := range []any{nil, true, "CANARY_RAW_BUDGET", int32(128), int64(128), uint(128), float64(128), json.Number("128"), map[string]any{"CANARY": "value"}} {
		t.Run(fmt.Sprintf("budget-type/%T", value), func(t *testing.T) {
			runThinkingControlModes(t, thinkingControlCase{
				model: "gemini-2.5-pro", options: llm.ModelOptions{"thinking_budget": value},
				thinking: llm.ThinkingConfig{Enabled: true, BudgetTokens: 128}, wantErr: google.ErrInvalidThinkingConfig,
			})
		})
	}
	for _, value := range []any{nil, true, 1, []any{"CANARY_RAW_LEVEL"}, "CANARY_RAW_LEVEL"} {
		t.Run(fmt.Sprintf("level-type/%T", value), func(t *testing.T) {
			runThinkingControlModes(t, thinkingControlCase{model: "gemini-4-hydrogen", options: llm.ModelOptions{"thinking_level": value}, wantErr: google.ErrInvalidThinkingConfig})
		})
	}
	for _, tc := range []struct {
		name string
		thinkingControlCase
	}{
		{"raw overrides invalid typed", thinkingControlCase{model: "gemini-2.5-flash", options: llm.ModelOptions{"thinking_budget": 128}, thinking: llm.ThinkingConfig{BudgetTokens: math.MaxInt}, wantBudget: new(128)}},
		{"raw dynamic overrides typed", thinkingControlCase{model: "gemini-2.5-pro", options: llm.ModelOptions{"thinking_budget": -1}, thinking: llm.ThinkingConfig{BudgetTokens: 128}, wantBudget: new(-1)}},
		{"raw off overrides typed", thinkingControlCase{model: "gemini-2.5-flash-lite", options: llm.ModelOptions{"thinking_budget": 0}, thinking: llm.ThinkingConfig{BudgetTokens: 512}, wantBudget: new(0)}},
		{"raw invalid overrides typed", thinkingControlCase{model: "gemini-2.5-pro", options: llm.ModelOptions{"thinking_budget": 127}, thinking: llm.ThinkingConfig{BudgetTokens: 128}, wantErr: google.ErrInvalidThinkingConfig}},
		{"raw off rejected despite typed", thinkingControlCase{model: "gemini-2.5-pro", options: llm.ModelOptions{"thinking_budget": 0}, thinking: llm.ThinkingConfig{BudgetTokens: 128}, wantErr: google.ErrInvalidThinkingConfig}},
		{"raw valid ignores typed dynamic", thinkingControlCase{model: "gemini-2.5-flash", options: llm.ModelOptions{"thinking_budget": 128}, thinking: llm.ThinkingConfig{BudgetTokens: -1}, wantBudget: new(128)}},
		{"raw valid ignores typed below dynamic", thinkingControlCase{model: "gemini-2.5-flash", options: llm.ModelOptions{"thinking_budget": 128}, thinking: llm.ThinkingConfig{BudgetTokens: -2}, wantBudget: new(128)}},
	} {
		t.Run(tc.name, func(t *testing.T) { runThinkingControlModes(t, tc.thinkingControlCase) })
	}
	for _, value := range []any{nil, 0, -1, 128, "CANARY_CONFLICT"} {
		t.Run(fmt.Sprintf("raw-conflict/%v", value), func(t *testing.T) {
			runThinkingControlModes(t, thinkingControlCase{model: "gemini-4-hydrogen", options: llm.ModelOptions{"thinking_budget": value, "thinking_level": "low"}, wantErr: google.ErrInvalidThinkingConfig})
		})
	}
	for _, tokens := range []int{-2, -1, 1, 128} {
		t.Run(fmt.Sprintf("typed-conflict/%d", tokens), func(t *testing.T) {
			runThinkingControlModes(t, thinkingControlCase{model: "gemini-4-hydrogen", options: llm.ModelOptions{"thinking_level": "low"}, thinking: llm.ThinkingConfig{BudgetTokens: tokens}, wantErr: google.ErrInvalidThinkingConfig})
		})
	}
}

func TestThinkingOmissionAndExactModelRecognition(t *testing.T) {
	for _, model := range []string{
		"", "gemini-2.5-flash", "gemini-2.5-flash-lite", "gemini-2.5-pro", "gemini-3.8-flash", "gemini-3.6-flash", "gemini-3.5-flash", "gemini-3.5-flash-lite",
		"gemini-4-argon", "gemini-4-carbon", "gemini-4-hydrogen", "gemini-3-pro-preview", "gemini-3.1-pro-preview", "gemini-3-flash-preview", "gemini-3.1-flash-lite-preview", "gemini-3.1-flash-lite",
		"unknown-CANARY_MODEL", "gemini-5-flash", "gemini-4-hydrogen-preview", "gemini-3.8-flash-001", "gemini-2.5-flash-image", "gemini-3.5-flash-cyber", "gemini-flash-latest",
		"GEMINI-4-HYDROGEN", " gemini-2.5-flash", "gemini-2.5-flash ", "models/models/gemini-2.5-flash", "custom/gemini-4-hydrogen",
	} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%t", model, enabled), func(t *testing.T) {
				runThinkingControlModes(t, thinkingControlCase{model: model, thinking: llm.ThinkingConfig{Enabled: enabled}})
			})
		}
	}
	for _, model := range []string{
		"unknown-CANARY_MODEL", "gemini-5-flash", "gemini-4-hydrogen-preview", "gemini-3.8-flash-001", "gemini-2.5-flash-image", "gemini-3.5-flash-cyber", "gemini-flash-latest",
		"GEMINI-4-HYDROGEN", " gemini-2.5-flash", "gemini-2.5-flash ", "models/models/gemini-2.5-flash", "custom/gemini-4-hydrogen",
	} {
		for _, prefix := range []string{"", "models/"} {
			for _, opts := range []llm.ModelOptions{{"thinking_budget": -1}, {"thinking_budget": 0}, {"thinking_budget": 128}, {"thinking_level": "low"}} {
				t.Run(fmt.Sprintf("%s%s/%v", prefix, model, opts), func(t *testing.T) {
					runThinkingControlModes(t, thinkingControlCase{model: prefix + model, options: opts, wantErr: google.ErrInvalidThinkingConfig})
				})
			}
			for _, tokens := range []int{-1, 128} {
				t.Run(fmt.Sprintf("%s%s/typed=%d", prefix, model, tokens), func(t *testing.T) {
					runThinkingControlModes(t, thinkingControlCase{model: prefix + model, thinking: llm.ThinkingConfig{BudgetTokens: tokens}, wantErr: google.ErrInvalidThinkingConfig})
				})
			}
		}
	}
}

func TestThinkingCancellationPrecedesInvalidControls(t *testing.T) {
	for _, backend := range []genai.Backend{genai.BackendGeminiAPI, genai.BackendVertexAI} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", backend, stream), func(t *testing.T) {
				provider, calls, hooks := thinkingTestProvider(t, backend, stream, "CANARY_MODEL", nil)
				ctx, cancel := context.WithCancelCause(t.Context())
				cause := errors.New("test cancellation cause")
				completion := provider.Complete(ctx, llm.CompletionRequest{Stream: stream, Options: llm.ModelOptions{"thinking_budget": "CANARY_BUDGET"}})
				cancel(cause)
				count := 0
				for chunk, err := range completion {
					count++
					if !errors.Is(err, cause) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
						t.Fatalf("yield = %+v, %v; want zero chunk and cancellation cause", chunk, err)
					}
				}
				if count != 1 || *calls != 0 || *hooks != 0 {
					t.Fatalf("yields=%d HTTP=%d hooks=%d", count, *calls, *hooks)
				}
			})
		}
	}
}

// Every case uses the actual SDK backend converter and both native operations.
func runThinkingControlModes(t *testing.T, tc thinkingControlCase) {
	t.Helper()
	for _, backend := range []genai.Backend{genai.BackendGeminiAPI, genai.BackendVertexAI} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", backend, stream), func(t *testing.T) {
				var thinking map[string]any
				provider, calls, hooks := thinkingTestProvider(t, backend, stream, tc.model, &thinking)
				req := llm.CompletionRequest{Stream: stream, Messages: []llm.Message{{Role: llm.RoleUser, Content: "CANARY_MESSAGE"}}, Options: tc.options, Thinking: tc.thinking}
				before := req
				before.Messages, before.Options = llm.CloneMessages(req.Messages), req.Options.Clone()
				completion := provider.Complete(t.Context(), req)
				if *calls != 0 || *hooks != 0 {
					t.Fatal("Complete performed eager work")
				}
				yields, failures := 0, 0
				for chunk, err := range completion {
					yields++
					if err != nil {
						failures++
						if tc.wantErr == nil || !errors.Is(err, tc.wantErr) || !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
							t.Fatalf("yield = %+v, %v; want zero chunk and %v", chunk, err, tc.wantErr)
						}
						for _, canary := range []string{"CANARY", tc.model} {
							if canary != "" && strings.Contains(err.Error(), canary) {
								t.Fatalf("error exposes input: %v", err)
							}
						}
					}
				}
				if !reflect.DeepEqual(req, before) {
					t.Fatal("Complete mutated borrowed request")
				}
				if tc.wantErr != nil {
					if yields != 1 || failures != 1 || *calls != 0 || *hooks != 0 {
						t.Fatalf("invalid control: yields=%d errors=%d HTTP=%d hooks=%d", yields, failures, *calls, *hooks)
					}
					return
				}
				if failures != 0 || *calls != 1 || *hooks != 1 {
					t.Fatalf("valid control: errors=%d HTTP=%d hooks=%d", failures, *calls, *hooks)
				}
				want := map[string]any{"includeThoughts": true}
				if tc.wantBudget != nil {
					want["thinkingBudget"] = float64(*tc.wantBudget)
				}
				if tc.wantLevel != "" {
					want["thinkingLevel"] = tc.wantLevel
				}
				if !reflect.DeepEqual(thinking, want) {
					t.Fatalf("thinking config = %#v, want %#v", thinking, want)
				}
			})
		}
	}
}

// Vertex uses API-key Express mode, not ADC or an injected GeminiAPI client.
// Its real endpoint and publishers/google model path prove backend dispatch;
// all HTTP is intercepted synchronously, without credentials or network access.
func thinkingTestProvider(t *testing.T, backend genai.Backend, stream bool, model string, thinking *map[string]any) (*google.Provider, *int, *int) {
	t.Helper()
	calls, hooks := new(int), new(int)
	host, version := "generativelanguage.googleapis.com", "v1beta"
	if backend == genai.BackendVertexAI {
		host, version = "aiplatform.googleapis.com", "v1beta1"
	}
	transport := &recordingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		*calls++
		wireModel := model
		if wireModel == "" {
			wireModel = "gemini-3.8-flash"
		}
		if !strings.HasPrefix(wireModel, "models/") {
			if backend == genai.BackendVertexAI {
				if strings.Contains(wireModel, "/") {
					publisher, name, _ := strings.Cut(wireModel, "/")
					wireModel = "publishers/" + publisher + "/models/" + name
				} else {
					wireModel = "publishers/google/models/" + wireModel
				}
			} else {
				wireModel = "models/" + wireModel
			}
		}
		operation := "generateContent"
		if stream {
			operation = "streamGenerateContent"
		}
		wantPath := "/" + version + "/" + wireModel + ":" + operation
		if req.Method != http.MethodPost || req.URL.Scheme != "https" || req.URL.Host != host || req.URL.Path != wantPath {
			t.Fatalf("backend request = %s %s; want https://%s%s", req.Method, req.URL, host, wantPath)
		}
		if stream && req.URL.Query().Get("alt") != "sse" {
			t.Fatalf("stream query = %s, want alt=sse", req.URL.RawQuery)
		}
		var body struct {
			GenerationConfig struct {
				ThinkingConfig map[string]any `json:"thinkingConfig"`
			} `json:"generationConfig"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if thinking != nil {
			*thinking = body.GenerationConfig.ThinkingConfig
		}
		contentType, response := "application/json", `{"candidates":[{"finishReason":"STOP"}]}`
		if stream {
			contentType, response = "text/event-stream", "data: "+response+"\n\n"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
	}}
	client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		APIKey: "test-only-key", Backend: backend, HTTPClient: &http.Client{Transport: transport},
		HTTPOptions: genai.HTTPOptions{BaseURL: "https://" + host, APIVersion: version, ExtrasRequestProvider: func(body map[string]any) map[string]any {
			*hooks++
			return body
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.ClientConfig().Backend != backend {
		t.Fatalf("client backend = %s, want %s", client.ClientConfig().Backend, backend)
	}
	opts := []options.Option[google.Provider]{google.WithClient(client)}
	if model != "" {
		opts = append(opts, google.WithModel(model))
	}
	provider, err := google.NewProvider("", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return provider, calls, hooks
}
