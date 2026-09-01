package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestStuckRecoveryScripted(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "-scripted", "-summary")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run stuck_recovery: %v\n%s", err, out)
	}

	text := string(out)
	for _, want := range []string{
		"status=succeeded",
		"Search attempts: 4",
		`Patterns tried:`,
		`"NonExistentHandler"`,
		"Confirmed: NonExistentHandler does not exist in codebase",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}
