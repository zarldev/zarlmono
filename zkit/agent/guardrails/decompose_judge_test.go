package guardrails_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// TestLLMVerdictJudge_ParsesEachAction asserts the judge round-trips
// each of the four valid actions out of a streaming JSON response.
// One subtest per enum value catches enum/spelling drift between the
// guardrail constants and what the judge accepts on the wire.
func TestLLMVerdictJudge_ParsesEachAction(t *testing.T) {
	cases := []guardrails.VerdictAction{
		guardrails.ActionRetryUnchanged,
		guardrails.ActionSmallerScope,
		guardrails.ActionSwitchTool,
		guardrails.ActionSpawnSubagent,
	}
	for _, action := range cases {
		t.Run(string(action), func(t *testing.T) {
			rationale := fmt.Sprintf("test rationale for %s", action)
			srv := httptest.NewServer(verdictHandler(t, string(action), rationale, nil))
			defer srv.Close()

			judge := newTestJudge(t, srv.URL)
			v, err := judge.Judge(context.Background(), guardrails.VerdictInput{
				Tool:     tools.ToolName("write"),
				Args:     tools.ToolParameters{"path": "x.go"},
				Error:    "boom",
				Attempts: 3,
			})
			if err != nil {
				t.Fatalf("Judge: %v", err)
			}
			if v.Action != action {
				t.Errorf("Action = %q, want %q", v.Action, action)
			}
			if v.Rationale != rationale {
				t.Errorf("Rationale = %q, want %q", v.Rationale, rationale)
			}
		})
	}
}

// TestLLMVerdictJudge_RequestCarriesResponseFormat verifies the wire
// shape: when the judge fires, the outgoing chat completion body
// includes a response_format with the verdict's enum, name, strict,
// and rationale-first property ordering. If this contract breaks,
// llama.cpp's grammar converter silently drops the constraint and
// the model can confabulate actions again.
func TestLLMVerdictJudge_RequestCarriesResponseFormat(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(verdictHandler(t, string(guardrails.ActionRetryUnchanged), "ok", &captured))
	defer srv.Close()

	judge := newTestJudge(t, srv.URL)
	_, err := judge.Judge(context.Background(), guardrails.VerdictInput{
		Tool:     tools.ToolName("write"),
		Error:    "boom",
		Attempts: 3,
	})
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(captured, &body); err != nil {
		t.Fatalf("unmarshal request body: %v\nbody: %s", err, captured)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong type: %v", body["response_format"])
	}
	if rf["type"] != "json_schema" {
		t.Errorf("response_format.type = %v, want json_schema", rf["type"])
	}
	inner, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("response_format.json_schema missing: %v", rf["json_schema"])
	}
	if inner["name"] != "decompose_verdict" {
		t.Errorf("name = %v, want decompose_verdict", inner["name"])
	}
	if inner["strict"] != true {
		t.Errorf("strict = %v, want true", inner["strict"])
	}
	schema, ok := inner["schema"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing: %v", inner["schema"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema.properties missing: %v", schema)
	}
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("schema.properties.action missing: %v", props)
	}
	enum, ok := action["enum"].([]any)
	if !ok {
		t.Fatalf("action.enum missing: %v", action)
	}
	wantEnum := map[string]bool{
		string(guardrails.ActionRetryUnchanged): false,
		string(guardrails.ActionSmallerScope):   false,
		string(guardrails.ActionSwitchTool):     false,
		string(guardrails.ActionSpawnSubagent):  false,
	}
	for _, v := range enum {
		if s, ok := v.(string); ok {
			if _, want := wantEnum[s]; want {
				wantEnum[s] = true
			}
		}
	}
	for k, seen := range wantEnum {
		if !seen {
			t.Errorf("enum missing %q", k)
		}
	}
}

// TestLLMVerdictJudge_StripsThinking checks the Qwen-style path: a
// <think>...</think> block prefixed onto the constrained JSON must
// be stripped before parsing, otherwise json.Unmarshal fails and we
// fall back unnecessarily.
func TestLLMVerdictJudge_StripsThinking(t *testing.T) {
	body := `<think>The user is stuck.</think>{"rationale":"flaky network","action":"retry_unchanged"}`
	srv := httptest.NewServer(rawStreamHandler(body))
	defer srv.Close()

	judge := newTestJudge(t, srv.URL)
	v, err := judge.Judge(context.Background(), guardrails.VerdictInput{
		Tool: "bash", Error: "net err", Attempts: 3,
	})
	if err != nil {
		t.Fatalf("Judge: %v (thinking should be stripped)", err)
	}
	if v.Action != guardrails.ActionRetryUnchanged {
		t.Errorf("Action = %q, want retry_unchanged", v.Action)
	}
}

// TestLLMVerdictJudge_RejectsInvalidAction guards the validation
// layer: even if a non-grammar-capable backend somehow emits a bogus
// enum value, the judge must surface an error so the guardrail
// falls back to the deterministic advisory rather than emitting
// nonsense.
func TestLLMVerdictJudge_RejectsInvalidAction(t *testing.T) {
	body := `{"rationale":"made up","action":"definitely_not_an_action"}`
	srv := httptest.NewServer(rawStreamHandler(body))
	defer srv.Close()

	judge := newTestJudge(t, srv.URL)
	_, err := judge.Judge(context.Background(), guardrails.VerdictInput{
		Tool: "bash", Error: "x", Attempts: 3,
	})
	if err == nil {
		t.Fatal("Judge: want error for invalid action, got nil")
	}
	if !strings.Contains(err.Error(), "invalid action") {
		t.Errorf("err = %v, want it to mention 'invalid action'", err)
	}
}

// TestLLMVerdictJudge_RejectsMalformedJSON catches the case where
// the server returned non-JSON (e.g. an error message in plain text
// because grammar constraint was rejected). The guardrail then falls
// back; the judge's job is just to fail loudly.
func TestLLMVerdictJudge_RejectsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(rawStreamHandler("not json at all"))
	defer srv.Close()

	judge := newTestJudge(t, srv.URL)
	_, err := judge.Judge(context.Background(), guardrails.VerdictInput{
		Tool: "bash", Error: "x", Attempts: 3,
	})
	if err == nil {
		t.Fatal("Judge: want error for malformed JSON, got nil")
	}
}

// newTestJudge builds an LLMVerdictJudge backed by an OpenAI-shaped
// provider pointed at the test server. Centralised so each subtest
// reads cleanly.
func newTestJudge(t *testing.T, baseURL string) *guardrails.LLMVerdictJudge {
	t.Helper()
	p, err := openai.NewProvider("test-key", openai.WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return guardrails.NewLLMVerdictJudge(p)
}

// verdictHandler returns an HTTP handler that ACK's a chat completion
// request with a single SSE chunk whose .content is the JSON envelope
// {"rationale": ..., "action": <action>}. When capture is non-nil
// the handler also stores the request body for wire-shape assertions.
func verdictHandler(t *testing.T, action, rationale string, capture *[]byte) http.HandlerFunc {
	t.Helper()
	envelope, err := json.Marshal(struct {
		Rationale string `json:"rationale"`
		Action    string `json:"action"`
	}{Rationale: rationale, Action: action})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return rawStreamCapturingHandler(string(envelope), capture)
}

// rawStreamHandler returns a handler that emits body verbatim as a
// single SSE content chunk, then [DONE]. Used to inject arbitrary
// content (including malformed JSON) for the error-path tests.
func rawStreamHandler(body string) http.HandlerFunc {
	return rawStreamCapturingHandler(body, nil)
}

func rawStreamCapturingHandler(body string, capture *[]byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			b, _ := io.ReadAll(r.Body)
			*capture = b
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Embed body as the .content of a single delta chunk. JSON
		// escape so quotes / newlines don't break the SSE frame.
		escaped, _ := json.Marshal(body)
		chunk := fmt.Sprintf(
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":%s},"finish_reason":"stop","index":0}]}`+"\n\n",
			escaped,
		)
		_, _ = w.Write([]byte(chunk))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}
