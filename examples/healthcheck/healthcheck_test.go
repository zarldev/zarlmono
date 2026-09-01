package main_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthcheckScriptedCLI(t *testing.T) {
	binary := buildHealthcheck(t)
	cmd := exec.Command(binary, "-scripted", "-attempts=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("healthcheck -scripted: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{
		"status=succeeded",
		"attempts=1",
		"provider=scripted",
		"api:healthy",
		"db:healthy",
		"cache:healthy",
		"checked=[api db cache]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not contain %q", got, want)
		}
	}
}

func TestHealthcheckCLIRejectsUnknownProvider(t *testing.T) {
	binary := buildHealthcheck(t)
	cmd := exec.Command(binary, "-provider=not-a-provider")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("healthcheck unexpectedly accepted an unknown provider:\n%s", output)
	}
	if !strings.Contains(string(output), "llm:") || !strings.Contains(string(output), "not-a-provider") {
		t.Fatalf("output = %q; want contextual unknown-provider error", output)
	}
}

func buildHealthcheck(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "healthcheck")
	cmd := exec.Command("go", "build", "-race", "-o", binary, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build healthcheck: %v\n%s", err, output)
	}
	return binary
}
