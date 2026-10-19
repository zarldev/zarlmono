package claudecode_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
)

func TestProviderResponseFormatDirective(t *testing.T) {
	cases := []struct {
		name     string
		format   llm.ResponseFormat
		tools    []llm.Tool
		contains []string
		absent   string
	}{
		{name: "text default", absent: "valid JSON object"},
		{name: "JSON object", format: llm.ResponseFormat{Type: llm.ResponseFormatJSONObject}, contains: []string{"single valid JSON object"}},
		{name: "JSON schema with host tools", format: llm.ResponseFormat{
			Type: llm.ResponseFormatJSONSchema,
			Schema: llm.SchemaFromMap(map[string]any{
				"type":       "object",
				"properties": map[string]any{"verdict": map[string]any{"type": "string"}},
			}),
		}, tools: []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "lookup"}}}, contains: []string{"conforms to this JSON Schema", `"verdict"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, prompt := complete(t, "", llm.CompletionRequest{
				Messages:       []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
				ResponseFormat: tc.format,
				Tools:          tc.tools,
			})
			for _, want := range tc.contains {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt missing %q in:\n%s", want, prompt)
				}
			}
			if tc.absent != "" && strings.Contains(prompt, tc.absent) {
				t.Errorf("prompt unexpectedly contains %q in:\n%s", tc.absent, prompt)
			}
		})
	}
}

func TestProviderUsesNativeJSONSchemaAndStructuredOutput(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	promptPath := filepath.Join(dir, "prompt")
	scriptPath := filepath.Join(dir, "claude")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$CLAUDE_TEST_ARGS"
cat > "$CLAUDE_TEST_PROMPT"
cat <<'OUTPUT'
{"type":"result","subtype":"success","structured_output":{"verdict":"pass"},"usage":{"input_tokens":2,"output_tokens":1}}
OUTPUT
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	t.Setenv("CLAUDE_TEST_ARGS", argsPath)
	t.Setenv("CLAUDE_TEST_PROMPT", promptPath)
	provider := claudecode.NewProvider(
		claudecode.StaticTokenSource{T: claudecode.Token{Access: "test-token"}},
		claudecode.WithBinaryPath(scriptPath),
	)
	schema := llm.SchemaFromMap(map[string]any{
		"type":       "object",
		"properties": map[string]any{"verdict": map[string]any{"type": "string"}},
		"required":   []any{"verdict"},
	})
	var content strings.Builder
	var last llm.CompletionChunk
	for chunk, err := range provider.Complete(t.Context(), llm.CompletionRequest{
		Messages:       []llm.Message{{Role: llm.RoleUser, Content: "judge"}},
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: schema},
	}) {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		content.WriteString(chunk.Content)
		last = chunk
	}
	if content.String() != `{"verdict":"pass"}` {
		t.Fatalf("content = %q, want structured output", content.String())
	}
	if last.FinishReason != llm.FinishReasons.STOP || !last.UsageReported {
		t.Fatalf("terminal metadata = %+v", last)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if !strings.Contains(string(args), "--json-schema\n") || !strings.Contains(string(args), `"verdict"`) {
		t.Fatalf("CLI args missing native schema: %s", args)
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if strings.Contains(string(prompt), "JSON Schema") {
		t.Fatalf("native schema duplicated into prompt: %s", prompt)
	}
}

func TestProviderKeepsHostToolProtocolSeparateFromNativeSchema(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	promptPath := filepath.Join(dir, "prompt")
	scriptPath := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CLAUDE_TEST_ARGS\"\ncat > \"$CLAUDE_TEST_PROMPT\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	t.Setenv("CLAUDE_TEST_ARGS", argsPath)
	t.Setenv("CLAUDE_TEST_PROMPT", promptPath)
	provider := claudecode.NewProvider(
		claudecode.StaticTokenSource{T: claudecode.Token{Access: "test-token"}},
		claudecode.WithBinaryPath(scriptPath),
	)
	schema := llm.SchemaFromMap(map[string]any{
		"type":       "object",
		"properties": map[string]any{"verdict": map[string]any{"type": "string"}},
	})
	for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{
		Messages:       []llm.Message{{Role: llm.RoleUser, Content: "judge"}},
		Tools:          []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "lookup"}}},
		ResponseFormat: llm.ResponseFormat{Type: llm.ResponseFormatJSONSchema, Schema: schema},
	}) {
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if strings.Contains(string(args), "--json-schema") {
		t.Fatalf("host-tool request used native schema flag: %s", args)
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	for _, want := range []string{"Tool calling protocol:", "conforms to this JSON Schema", `"verdict"`} {
		if !strings.Contains(string(prompt), want) {
			t.Fatalf("host-tool prompt missing %q:\n%s", want, prompt)
		}
	}
}
