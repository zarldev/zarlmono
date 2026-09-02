package version_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/version"
)

func TestStringIsNonEmpty(t *testing.T) {
	t.Parallel()
	if strings.TrimSpace(version.String()) == "" {
		t.Fatal("version is empty")
	}
}

func TestCLIPrintsVersion(t *testing.T) {
	t.Parallel()
	cmd := exec.CommandContext(t.Context(), "go", "run", "../cmd/eval", "-version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run -version: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) == "" {
		t.Fatal("version output is empty")
	}
}
