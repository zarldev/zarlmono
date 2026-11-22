package zarlcode_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRateCardCLI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{name: "omitted version", args: []string{"--version"}},
		{name: "version never reads missing file", args: []string{"--version", "--rate-card", "private-missing-canary"}},
		{name: "version rejects empty without services", args: []string{"--rate-card=", "--version"}, code: 4},
		{name: "invalid path rejects before persistence", args: []string{"--headless", "--rate-card=private-missing-canary"}, code: 1},
		{name: "explicit empty rejects before services", args: []string{"--rate-card="}, code: 4},
		{name: "headless prompt gate retained", args: []string{"--headless", "--rate-card=private-missing-canary", "--prompt-file=missing", "--prompt-text=conflict"}, code: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			cmd := exec.CommandContext(t.Context(), os.Args[0], tc.args...)
			// Reuse the existing external entrypoint process seam.
			cmd.Env = append(os.Environ(), "ZARLCODE_PROTOCOL_CLI_PROCESS=1", "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_DATA_HOME="+filepath.Join(home, "data"))
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
				t.Fatalf("exit=%d want=%d output=%s", code, tc.code, output)
			}
			if strings.Contains(string(output), "private-missing-canary") {
				t.Fatal("private path echoed")
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 0 {
				t.Fatalf("CLI parsing/version reached persistence: %v %v", entries, err)
			}
		})
	}
}
