package toolparse_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/toolparse"
)

func noparse(t *testing.T, content string) {
	t.Helper()
	res := toolparse.ParseArtifact(content)
	if len(res.Calls) > 0 {
		t.Errorf("expected no parses for %q, got %d calls (shape=%v)", content, len(res.Calls), res.Shape)
	}
}

func mustparse(t *testing.T, content string) toolparse.Result {
	t.Helper()
	res := toolparse.ParseArtifact(content)
	if len(res.Calls) == 0 {
		t.Fatalf("expected parse for %q, got none (shape=%v)", content, res.Shape)
	}
	if !res.HighConfidence {
		t.Errorf("expected high confidence for %q", content)
	}
	return res
}

func firstCallName(t *testing.T, calls []llm.ToolCall) string {
	t.Helper()
	if len(calls) == 0 {
		t.Fatal("no tool calls")
	}
	return calls[0].Function.Name
}

func argAsMap(t *testing.T, args string) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		t.Fatalf("unmarshal args %q: %v", args, err)
	}
	return m
}

// --- Tagged assistant_tool_calls ---

func TestParse_TaggedAssistantToolCalls(t *testing.T) {
	res := mustparse(t, `<assistant_tool_calls>[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"foo.go\"}"}}]</assistant_tool_calls>`)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "read" {
		t.Errorf("name = %s, want read", g)
	}
	m := argAsMap(t, res.Calls[0].Function.Arguments)
	if m["path"] != "foo.go" {
		t.Errorf("path = %s, want foo.go", m["path"])
	}
	if res.Shape != toolparse.ShapeTaggedAssistantCalls {
		t.Errorf("shape = %v, want ShapeTaggedAssistantCalls", res.Shape)
	}
}

func TestParse_TaggedToolCallsTag(t *testing.T) {
	res := mustparse(t, `<tool_calls>[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"bar.go\"}"}}]</tool_calls>`)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "read" {
		t.Errorf("name = %s, want read", g)
	}
	if res.Shape != toolparse.ShapeTaggedToolCalls {
		t.Errorf("shape = %v, want ShapeTaggedToolCalls", res.Shape)
	}
}

func TestParse_TaggedToolCallsFlatShape(t *testing.T) {
	res := mustparse(t, `<tool_calls>[{"id":"c1","name":"read","arguments":{"path":"bar.go"}}]</tool_calls>`)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "read" {
		t.Errorf("name = %s, want read", g)
	}
	m := argAsMap(t, res.Calls[0].Function.Arguments)
	if m["path"] != "bar.go" {
		t.Errorf("path = %s, want bar.go", m["path"])
	}
}

func assertUniqueIDs(t *testing.T, calls []llm.ToolCall) {
	t.Helper()
	seen := make(map[string]bool, len(calls))
	for i, c := range calls {
		if c.ID == "" {
			t.Errorf("call %d has empty ID", i)
		}
		if seen[c.ID] {
			t.Errorf("duplicate call ID %q at index %d", c.ID, i)
		}
		seen[c.ID] = true
	}
}

// Two separate tagged blocks, each with a call that omits its ID, used to
// collide: every block restarted its own call_0 counter, so the runner (which
// keys tool calls by ID) silently dropped the second. assignCallIDs must hand
// out distinct synthetic IDs across the merged set.
func TestParse_MultipleTaggedBlocksMissingIDs(t *testing.T) {
	content := `<tool_calls>[{"name":"read","arguments":{"path":"a.go"}}]</tool_calls>` +
		`<tool_calls>[{"name":"read","arguments":{"path":"b.go"}}]</tool_calls>`
	res := mustparse(t, content)
	if n := len(res.Calls); n != 2 {
		t.Fatalf("got %d calls, want 2", n)
	}
	assertUniqueIDs(t, res.Calls)
}

// A model that emits two calls under the same literal ID would also be
// collapsed to one by the runner. The duplicate must be renumbered.
func TestParse_DuplicateLiteralIDs(t *testing.T) {
	content := `{"tool_calls":[` +
		`{"id":"dup","name":"read","arguments":{"path":"a.go"}},` +
		`{"id":"dup","name":"read","arguments":{"path":"b.go"}}]}`
	res := mustparse(t, content)
	if n := len(res.Calls); n != 2 {
		t.Fatalf("got %d calls, want 2", n)
	}
	assertUniqueIDs(t, res.Calls)
}

// --- Protocol object {"tool_calls": [...]} ---

func TestParse_ProtocolObject(t *testing.T) {
	res := mustparse(t, `{"tool_calls":[{"id":"c1","name":"write","arguments":{"path":"out.txt","content":"hello"}}]}`)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "write" {
		t.Errorf("name = %s, want write", g)
	}
	if res.Shape != toolparse.ShapeProtocolObject {
		t.Errorf("shape = %v, want ShapeProtocolObject", res.Shape)
	}
}

func TestParse_ProtocolObjectWithPreamble(t *testing.T) {
	content := `Let me read that file for you.
{"tool_calls":[{"id":"c1","name":"read","arguments":{"path":"README.md"}}]}`
	res := mustparse(t, content)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "read" {
		t.Errorf("name = %s, want read", g)
	}
	if res.Shape != toolparse.ShapeProtocolObject {
		t.Errorf("shape = %v, want ShapeProtocolObject", res.Shape)
	}
	if res.RemainingContent == "" {
		t.Error("expected remaining content (the preamble) to be non-empty")
	}
}

// --- Bare nested-function array ---

func TestParse_BareNestedFunctionArray(t *testing.T) {
	res := mustparse(t, `[{"id":"c1","type":"function","function":{"name":"ls","arguments":"{\"path\":\".\"}"}}]`)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "ls" {
		t.Errorf("name = %s, want ls", g)
	}
	if res.Shape != toolparse.ShapeBareNestedArray {
		t.Errorf("shape = %v, want ShapeBareNestedArray", res.Shape)
	}
}

// --- Bare flat array ---

func TestParse_BareFlatArray(t *testing.T) {
	res := mustparse(t, `[{"id":"c1","name":"bash","arguments":{"command":"echo hello"}}]`)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "bash" {
		t.Errorf("name = %s, want bash", g)
	}
	if res.Shape != toolparse.ShapeBareFlatArray {
		t.Errorf("shape = %v, want ShapeBareFlatArray", res.Shape)
	}
}

// --- No parse cases ---

func TestParse_EmptyString(t *testing.T) {
	noparse(t, "")
}

func TestParse_PlainProse(t *testing.T) {
	noparse(t, "Hello, I am an AI assistant. How can I help you today?")
}

func TestParse_ProseMentioningToolCalls(t *testing.T) {
	noparse(t, "I will call the read tool to check the file contents. The tool_calls field will contain the function invocations.")
}

func TestParse_JustABrace(t *testing.T) {
	noparse(t, "{")
}

func TestParse_EmptyTaggedBlock(t *testing.T) {
	noparse(t, "<tool_calls></tool_calls>")
}

func TestParse_EmptyArray(t *testing.T) {
	noparse(t, "[]")
}

// --- Argument normalization ---

func TestParse_NormalizesNestedArgs(t *testing.T) {
	res := mustparse(t, `<tool_calls>[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"foo.go\"}"}}]</tool_calls>`)
	m := argAsMap(t, res.Calls[0].Function.Arguments)
	if m["path"] != "foo.go" {
		t.Errorf("path = %s, want foo.go", m["path"])
	}
}

func TestParse_NormalizesDirectObjectArgs(t *testing.T) {
	res := mustparse(t, `<tool_calls>[{"id":"c1","name":"read","arguments":{"path":"bar.go"}}]</tool_calls>`)
	m := argAsMap(t, res.Calls[0].Function.Arguments)
	if m["path"] != "bar.go" {
		t.Errorf("path = %s, want bar.go", m["path"])
	}
}

// --- Id generation for missing IDs ---

func TestParse_GeneratesIDs(t *testing.T) {
	res := mustparse(t, `<tool_calls>[{"name":"read","arguments":{"path":"a.go"}},{"name":"write","arguments":{"path":"b.go"}}]</tool_calls>`)
	if n := len(res.Calls); n != 2 {
		t.Fatalf("got %d calls, want 2", n)
	}
	if id := res.Calls[0].ID; id == "" {
		t.Error("first call missing ID")
	}
	if id := res.Calls[1].ID; id == "" {
		t.Error("second call missing ID")
	}
	if res.Calls[0].ID == res.Calls[1].ID {
		t.Error("both calls got the same synthesized ID")
	}
}

// --- Whitespace trimming ---

func TestParse_TrimsWhitespace(t *testing.T) {
	res := mustparse(t, "  \n  <tool_calls>[{\"name\":\"read\",\"arguments\":{\"path\":\"a.go\"}}]</tool_calls>  \n  ")
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "read" {
		t.Errorf("name = %s, want read", g)
	}
}

// --- IsToolCallArtifactPrefix ---

func TestIsToolCallArtifactPrefix(t *testing.T) {
	cases := []struct {
		content string
		want    bool
	}{
		{"<tool_calls>[{...", true},
		{"<assistant_tool_calls>[{...", true},
		{`{"tool_calls":[{...`, true},
		{`[{"id":"call_1","name":"read"`, true},
		{`[{"name":"read"`, true},
		{`[{"function":{...`, true},
		{"Hello, I am...", false},
		{"Let me check...", false},
		{"", false},
		{"<random>x</random>", false},
		{"<tool_calls> works", true},
	}
	for _, c := range cases {
		got := toolparse.IsToolCallArtifactPrefix(c.content)
		if got != c.want {
			t.Errorf("IsToolCallArtifactPrefix(%q) = %v, want %v", c.content, got, c.want)
		}
	}
}

// --- Tagged with surrounding prose ---

func TestParse_TaggedWithSurroundingProse(t *testing.T) {
	content := `Let me read that file.
<tool_calls>[{"id":"c1","name":"read","arguments":{"path":"foo.go"}}]</tool_calls>
Then I will edit it.`
	res := mustparse(t, content)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "read" {
		t.Errorf("name = %s, want read", g)
	}
	if rem := res.RemainingContent; rem == "" {
		t.Error("expected remaining content from surrounding prose")
	}
}

// --- Multiple tagged blocks ---

func TestParse_MultipleTaggedBlocks(t *testing.T) {
	content := `<tool_calls>[{"name":"read","arguments":{"path":"a.go"}}]</tool_calls>
<tool_calls>[{"name":"write","arguments":{"path":"b.go"}}]</tool_calls>`
	res := mustparse(t, content)
	if n := len(res.Calls); n != 2 {
		t.Fatalf("got %d calls, want 2", n)
	}
	if g := res.Calls[0].Function.Name; g != "read" {
		t.Errorf("call 0 name = %s, want read", g)
	}
	if g := res.Calls[1].Function.Name; g != "write" {
		t.Errorf("call 1 name = %s, want write", g)
	}
}

// --- Fenced code blocks containing tool calls ---

func TestParse_FencedToolCalls(t *testing.T) {
	content := "```json\n{\"tool_calls\":[{\"id\":\"c1\",\"name\":\"read\",\"arguments\":{\"path\":\"foo.go\"}}]}\n```"
	res := mustparse(t, content)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "read" {
		t.Errorf("name = %s, want read", g)
	}
}

// --- Null arguments become {} ---

func TestParse_NullArgs(t *testing.T) {
	res := mustparse(t, `<tool_calls>[{"name":"read","arguments":null}]</tool_calls>`)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := res.Calls[0].Function.Arguments; g != "{}" {
		t.Errorf("null args should become {}, got %s", g)
	}
}

// --- Empty args ---

func TestParse_EmptyArgs(t *testing.T) {
	res := mustparse(t, `<tool_calls>[{"name":"ls","arguments":{}}]</tool_calls>`)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	args := strings.TrimSpace(res.Calls[0].Function.Arguments)
	if args == "" {
		t.Error("args should not be empty string")
	}
}

// --- OpenAI-shaped arguments (JSON-encoded string) ---

func TestParse_OpenAIWrappedArgs(t *testing.T) {
	res := mustparse(t, `<tool_calls>[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\": \"wrapped.go\"}"}}]</tool_calls>`)
	m := argAsMap(t, res.Calls[0].Function.Arguments)
	if m["path"] != "wrapped.go" {
		t.Errorf("path = %s, want wrapped.go", m["path"])
	}
}

// --- Fenced nested-function array ---

func TestParse_FencedNestedArray(t *testing.T) {
	content := "```\n[{\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"grep\",\"arguments\":\"{\\\"pattern\\\":\\\"foo\\\"}\"}}]\n```"
	res := mustparse(t, content)
	if n := len(res.Calls); n != 1 {
		t.Fatalf("got %d calls, want 1", n)
	}
	if g := firstCallName(t, res.Calls); g != "grep" {
		t.Errorf("name = %s, want grep", g)
	}
}
