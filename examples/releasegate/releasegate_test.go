package main_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleasegateScriptedCLI(t *testing.T) {
	binary := buildReleasegate(t)
	cmd := exec.Command(binary, "-scripted", "-attempts=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("releasegate -scripted: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{
		"status=succeeded",
		"attempts=1",
		"provider=scripted",
		"version=v1.2.3",
		"published=true",
		`channel="production"`,
		"notes_approved=true",
		"check tests=true",
		"notes approved by guardrail",
		"published to production",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not contain %q", got, want)
		}
	}
}

func TestReleasegateCLIRejectsUnknownProvider(t *testing.T) {
	binary := buildReleasegate(t)
	cmd := exec.Command(binary, "-provider=not-a-provider")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("releasegate unexpectedly accepted an unknown provider:\n%s", output)
	}
	if !strings.Contains(string(output), "llm:") || !strings.Contains(string(output), "not-a-provider") {
		t.Fatalf("output = %q; want contextual unknown-provider error", output)
	}
}

func buildReleasegate(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "releasegate")
	cmd := exec.Command("go", "build", "-race", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build releasegate: %v\n%s", err, output)
	}
	return binary
}
