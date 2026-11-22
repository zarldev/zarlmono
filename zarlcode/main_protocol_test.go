package zarlcode_test

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode"
)

func TestProtocolCLI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{name: "omitted"},
		{name: "chat_completions", args: []string{"--protocol=chat_completions"}},
		{name: "responses", args: []string{"--protocol", "responses"}},
		{name: "anthropic_messages", args: []string{"--protocol=anthropic_messages"}},
		{name: "generate_content", args: []string{"--protocol=generate_content"}},
		{name: "unknown", args: []string{"--protocol=unknown"}, code: 2},
		{name: "invalid", args: []string{"--protocol=not-a-protocol"}, code: 2},
		{name: "empty", args: []string{"--protocol="}, code: 2},
		{name: "no aliases", args: []string{"--protocol=messages"}, code: 2},
		{name: "case sensitive", args: []string{"--protocol=Responses"}, code: 2},
		{name: "no whitespace repair", args: []string{"--protocol= responses"}, code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			args := append([]string{"--version"}, tc.args...)
			cmd := exec.CommandContext(t.Context(), os.Args[0], args...)
			cmd.Env = append(os.Environ(), "ZARLCODE_PROTOCOL_CLI_PROCESS=1", "HOME="+home)
			output, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tc.code {
				t.Fatalf("exit = %d, want %d; output: %s", code, tc.code, output)
			}
			if tc.code != 0 && !strings.Contains(string(output), "for flag -protocol") {
				t.Fatalf("missing protocol diagnostic: %s", output)
			}
			entries, err := os.ReadDir(home)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("CLI parsing/version reached application setup: %v", entries)
			}
		})
	}
}

// TestMain invokes the real entrypoint in isolated child processes before the
// testing flags are parsed, without replacing process-global flag state.
func TestMain(m *testing.M) {
	if os.Getenv("ZARLCODE_PROTOCOL_CLI_PROCESS") == "1" {
		zarlcode.Main()
		return
	}
	os.Exit(m.Run())
}
