package dependencycheck_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/dependencycheck"
)

func TestRunVerifiesAllModules(t *testing.T) {
	root := dependencyFixture(t, "v1.68.0", "v0.14.0")
	var stdout, stderr bytes.Buffer

	err := dependencycheck.Run(t.Context(), root, &stdout, &stderr)

	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "examples swebench-eval zarlcode zkit") {
		t.Fatalf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunReportsResolvedMismatch(t *testing.T) {
	root := dependencyFixture(t, "v1.67.0", "v0.14.0")
	var stdout, stderr bytes.Buffer

	err := dependencycheck.Run(t.Context(), root, &stdout, &stderr)

	if !errors.Is(err, dependencycheck.ErrIncompatible) {
		t.Fatalf("Run() error = %v, want ErrIncompatible", err)
	}
	if got := stderr.String(); !strings.Contains(got, "examples: incompatible") || !strings.Contains(got, "v1.67.0") {
		t.Fatalf("stderr = %q", got)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func dependencyFixture(t *testing.T, anthropic, jsonschema string) string {
	t.Helper()
	root := t.TempDir()
	body := "module fixture\n\ngo 1.26\n\nrequire (\n\tgithub.com/anthropics/anthropic-sdk-go " + anthropic + "\n\tgithub.com/invopop/jsonschema " + jsonschema + "\n)\n"
	for _, module := range []string{"examples", "swebench-eval", "zarlcode", "zkit"} {
		dir := filepath.Join(root, module)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
