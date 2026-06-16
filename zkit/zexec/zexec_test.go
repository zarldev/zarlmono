package zexec_test

import (
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zexec"
)

func TestMinimalEnvIncludesOnlySafeDefaultsAndExtra(t *testing.T) {
	t.Setenv("PATH", "/bin")
	t.Setenv("SystemRoot", "C:/Windows")
	t.Setenv("WINDIR", "C:/Windows")
	t.Setenv("SECRET_TOKEN", "nope")

	env := zexec.MinimalEnv(map[string]string{
		"CUSTOM": "value",
		"PATH":   "/custom/bin",
	})

	got := envMap(env)
	if got["PATH"] != "/custom/bin" {
		t.Fatalf("PATH = %q, want extra override", got["PATH"])
	}
	if got["CUSTOM"] != "value" {
		t.Fatalf("CUSTOM = %q, want value", got["CUSTOM"])
	}
	if got["SECRET_TOKEN"] != "" {
		t.Fatalf("SECRET_TOKEN leaked into minimal env: %q", got["SECRET_TOKEN"])
	}
	if got["SystemRoot"] == "" || got["WINDIR"] == "" {
		t.Fatalf("windows defaults missing from minimal env: %v", got)
	}
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}
