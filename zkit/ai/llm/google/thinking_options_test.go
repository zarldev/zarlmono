package google_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

func TestExplicitThinkingControls(t *testing.T) {
	for _, tc := range []struct {
		name, model, want string
		options           llm.ModelOptions
		invalid           bool
	}{
		{"unspecified", "gemini-2.5-flash", "", nil, false},
		{"off", "gemini-2.5-flash", `"thinkingBudget":0`, llm.ModelOptions{"thinking_budget": 0}, false},
		{"lite off", "gemini-2.5-flash-lite", `"thinkingBudget":0`, llm.ModelOptions{"thinking_budget": 0}, false},
		{"pro cannot disable", "gemini-2.5-pro", "", llm.ModelOptions{"thinking_budget": 0}, true},
		{"pro minimum", "gemini-2.5-pro", `"thinkingBudget":128`, llm.ModelOptions{"thinking_budget": 128}, false},
		{"level", "gemini-3-flash-preview", `"thinkingLevel":"MINIMAL"`, llm.ModelOptions{"thinking_level": "minimal"}, false},
		{"pro minimal unsupported", "gemini-3-pro-preview", "", llm.ModelOptions{"thinking_level": "minimal"}, true},
		{"unknown future model", "gemini-3.8-flash", "", llm.ModelOptions{"thinking_level": "low"}, true},
		{"exclusive", "gemini-3-flash-preview", "", llm.ModelOptions{"thinking_level": "low", "thinking_budget": 0}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			transport := &recordingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
				data, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				body = string(data)
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")), Request: req}, nil
			}}
			provider, err := google.NewProvider("test", google.WithModel(tc.model), google.WithHTTPClient(&http.Client{Transport: transport}))
			if err != nil {
				t.Fatal(err)
			}
			var terminal error
			for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Options: tc.options}) {
				if err != nil {
					terminal = err
				}
			}
			if tc.invalid {
				if terminal == nil || body != "" {
					t.Fatalf("invalid control reached transport: %s, %v", body, terminal)
				}
				return
			}
			if terminal != nil {
				t.Fatal(terminal)
			}
			if tc.want != "" && !strings.Contains(body, tc.want) {
				t.Errorf("request = %s, want %s", body, tc.want)
			}
			if tc.want == "" && strings.Contains(body, "thinkingBudget") {
				t.Errorf("unspecified thinking was disabled: %s", body)
			}
		})
	}
}

func TestUserPartsTakePrecedence(t *testing.T) {
	var texts []string
	transport := &recordingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		var body struct {
			Contents []struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, content := range body.Contents {
			for _, part := range content.Parts {
				texts = append(texts, part.Text)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")), Request: req}, nil
	}}
	provider, err := google.NewProvider("test", google.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "duplicate", Parts: []llm.ContentPart{llm.TextPart("only parts")}},
		{Role: llm.RoleUser, Content: "empty parts suppress content", Parts: []llm.ContentPart{}},
		{Role: llm.RoleUser, Content: "fallback"},
	}}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(texts, "|") != "only parts|fallback" {
		t.Errorf("texts = %v", texts)
	}
}
