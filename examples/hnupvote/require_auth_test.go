package main_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHNUpvoteRejectsConfirmationWithoutCredentials(t *testing.T) {
	binary := buildHNUpvoteAuth(t)
	cmd := exec.Command(binary, "-confirm")
	cmd.Env = []string{"PATH=" + mustPath(t)}
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("hnupvote unexpectedly ran without provider credentials:\n%s", output)
	}
	if !strings.Contains(string(output), "llm:") {
		t.Fatalf("output = %q; want contextual provider error", output)
	}
}

func buildHNUpvoteAuth(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "hnupvote")
	cmd := exec.Command("go", "build", "-race", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build hnupvote: %v\n%s", err, output)
	}
	return binary
}

func mustPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(path)
}
