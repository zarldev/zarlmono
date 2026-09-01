package main_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHNUpvoteRequiresExplicitConfirmation(t *testing.T) {
	binary := buildHNUpvote(t)
	cmd := exec.Command(binary)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("hnupvote unexpectedly ran without confirmation:\n%s", output)
	}
	got := string(output)
	if !strings.Contains(got, "performs a real action on live Hacker News") || !strings.Contains(got, "pass -confirm") {
		t.Fatalf("output = %q; want explicit real-action confirmation guidance", got)
	}
}

func TestHNUpvoteHelpDescribesSafetyFlags(t *testing.T) {
	binary := buildHNUpvote(t)
	cmd := exec.Command(binary, "-help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hnupvote -help: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{
		"-confirm",
		"perform the real upvote on live Hacker News",
		"-headless",
		"-attempts",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help output %q does not contain %q", got, want)
		}
	}
}

func buildHNUpvote(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "hnupvote")
	cmd := exec.Command("go", "build", "-race", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build hnupvote: %v\n%s", err, output)
	}
	return binary
}
