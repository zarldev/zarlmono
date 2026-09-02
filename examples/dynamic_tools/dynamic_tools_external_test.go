package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestDynamicToolsLifecycle(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run dynamic_tools: %v\n%s", err, output)
	}

	got := strings.TrimSpace(string(output))
	want := strings.Join([]string{
		"authored_and_registered=echo_upper",
		"invoked=FIRST CALL",
		"reloaded_and_invoked=AFTER RELOAD",
		"collision=rejected",
		"unregistered=echo_upper",
		"catalog_entries=0",
	}, "\n")
	if got != want {
		t.Fatalf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
