package spawn_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/tools/spawn"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

// TestLLMSpawnPlanner_ParsesPlan exercises the happy-path: the
// planner's outgoing request is shaped right, the server's
// constrained JSON response round-trips into a SpawnPlan with the
// expected agent + mode + rationale.
func TestLLMSpawnPlanner_ParsesPlan(t *testing.T) {
	cases := []struct {
		mode    spawn.SpawnMode
		agent   string
		comment string
	}{
		{spawn.SpawnModeExplore, "researcher", "exploratory pick"},
		{spawn.SpawnModeImplement, "coder", "implementation pick"},
		{spawn.SpawnModeVerify, "reviewer", "verification pick"},
		{spawn.SpawnModeExplore, "", "parent-runner fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.comment, func(t *testing.T) {
			rationale := "test rationale " + tc.comment
			srv := httptest.NewServer(plannerHandler(t, tc.agent, string(tc.mode), rationale, nil))
			defer srv.Close()

			planner := newTestPlanner(t, srv.URL)
			out, err := planner.Plan(context.Background(), spawn.SpawnPlanInput{
				Prompt:          "do the thing",
				AvailableAgents: []string{"researcher", "coder", "reviewer"},
			})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if out.Agent != tc.agent {
				t.Errorf("Agent = %q, want %q", out.Agent, tc.agent)
			}
			if out.Mode != tc.mode {
				t.Errorf("Mode = %q, want %q", out.Mode, tc.mode)
			}
			if out.Rationale != rationale {
				t.Errorf("Rationale = %q, want %q", out.Rationale, rationale)
			}
		})
	}
}

// TestLLMSpawnPlanner_RequestCarriesDynamicEnum verifies the
// load-bearing property: the agent enum in the outgoing
// response_format includes EVERY registered name plus "" for
// parent fallback, and the schema is named/strict as expected.
// If this breaks, llama.cpp's grammar converter silently drops the
// constraint and the planner can pick names that don't resolve.
func TestLLMSpawnPlanner_RequestCarriesDynamicEnum(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(plannerHandler(t, "researcher", string(spawn.SpawnModeExplore), "ok", &captured))
	defer srv.Close()

	planner := newTestPlanner(t, srv.URL)
	_, err := planner.Plan(context.Background(), spawn.SpawnPlanInput{
		Prompt:          "trace request flow",
		AvailableAgents: []string{"researcher", "coder", "reviewer"},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
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
	inner := rf["json_schema"].(map[string]any)
	if inner["name"] != "spawn_plan" {
		t.Errorf("name = %v, want spawn_plan", inner["name"])
	}
	if inner["strict"] != true {
		t.Errorf("strict = %v, want true", inner["strict"])
	}
	schema := inner["schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	agent := props["agent"].(map[string]any)
	enum, ok := agent["enum"].([]any)
	if !ok {
		t.Fatalf("agent.enum missing or wrong type: %v", agent["enum"])
	}
	want := map[string]bool{
		"":           false,
		"researcher": false,
		"coder":      false,
		"reviewer":   false,
	}
	for _, v := range enum {
		if s, ok := v.(string); ok {
			if _, expected := want[s]; expected {
				want[s] = true
			}
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("enum missing %q", k)
		}
	}
}

// TestLLMSpawnPlanner_RejectsUnknownAgent guards the defensive
// validation: if the model returns an agent not in the supplied
// set (would only happen on a provider without grammar support),
// the planner surfaces an error so spawn.Tool falls back rather
// than dispatching to a name no resolver recognises.
func TestLLMSpawnPlanner_RejectsUnknownAgent(t *testing.T) {
	body := `{"rationale":"...","agent":"phantom","mode":"explore"}`
	srv := httptest.NewServer(rawStreamHandler(body))
	defer srv.Close()

	planner := newTestPlanner(t, srv.URL)
	_, err := planner.Plan(context.Background(), spawn.SpawnPlanInput{
		Prompt:          "x",
		AvailableAgents: []string{"researcher", "coder"},
	})
	if err == nil {
		t.Fatal("Plan: want error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "invalid agent") {
		t.Errorf("err = %v, want 'invalid agent'", err)
	}
}

// TestLLMSpawnPlanner_RejectsInvalidMode guards the same defensive
// validation on the mode field.
func TestLLMSpawnPlanner_RejectsInvalidMode(t *testing.T) {
	body := `{"rationale":"...","agent":"researcher","mode":"playtime"}`
	srv := httptest.NewServer(rawStreamHandler(body))
	defer srv.Close()

	planner := newTestPlanner(t, srv.URL)
	_, err := planner.Plan(context.Background(), spawn.SpawnPlanInput{
		Prompt:          "x",
		AvailableAgents: []string{"researcher"},
	})
	if err == nil {
		t.Fatal("Plan: want error for invalid mode, got nil")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("err = %v, want 'invalid mode'", err)
	}
}

// TestLLMSpawnPlanner_StripsThinking confirms a Qwen-style provider
// that prepends <think>...</think> doesn't break JSON parsing.
func TestLLMSpawnPlanner_StripsThinking(t *testing.T) {
	body := `<think>weighing the options.</think>{"rationale":"the task is read-only","agent":"researcher","mode":"explore"}`
	srv := httptest.NewServer(rawStreamHandler(body))
	defer srv.Close()

	planner := newTestPlanner(t, srv.URL)
	out, err := planner.Plan(context.Background(), spawn.SpawnPlanInput{
		Prompt:          "x",
		AvailableAgents: []string{"researcher"},
	})
	if err != nil {
		t.Fatalf("Plan: %v (thinking should be stripped)", err)
	}
	if out.Agent != "researcher" {
		t.Errorf("Agent = %q, want researcher", out.Agent)
	}
}

// TestLLMSpawnPlanner_RejectsEmptyAgentList guards against the
// nonsensical "constrain me to nothing" call. spawn.Tool gates this
// out before calling Plan, but the planner defends in depth.
func TestLLMSpawnPlanner_RejectsEmptyAgentList(t *testing.T) {
	planner := newTestPlanner(t, "http://localhost:65535")
	_, err := planner.Plan(context.Background(), spawn.SpawnPlanInput{
		Prompt:          "x",
		AvailableAgents: nil,
	})
	if err == nil {
		t.Fatal("Plan: want error for empty agents list, got nil")
	}
	if !strings.Contains(err.Error(), "no available agents") {
		t.Errorf("err = %v, want 'no available agents'", err)
	}
}

func newTestPlanner(t *testing.T, baseURL string) *spawn.LLMSpawnPlanner {
	t.Helper()
	p, err := openai.NewProvider("test-key", openai.WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return spawn.NewLLMSpawnPlanner(p)
}

// plannerHandler ACK's a chat completion with a single SSE chunk
// whose .content is the JSON envelope {rationale, agent, mode}.
// capture (if non-nil) records the inbound request body so wire-
// shape assertions can run after Plan returns.
func plannerHandler(t *testing.T, agent, mode, rationale string, capture *[]byte) http.HandlerFunc {
	t.Helper()
	envelope, err := json.Marshal(struct {
		Rationale string `json:"rationale"`
		Agent     string `json:"agent"`
		Mode      string `json:"mode"`
	}{Rationale: rationale, Agent: agent, Mode: mode})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return rawStreamCapturingHandler(string(envelope), capture)
}

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
		escaped, _ := json.Marshal(body)
		chunk := fmt.Sprintf(
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":%s},"finish_reason":"stop","index":0}]}`+"\n\n",
			escaped)
		_, _ = w.Write([]byte(chunk))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}
