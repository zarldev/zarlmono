package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSpawnWorkerScripted(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "run", ".", "-scripted", "-show-files")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run spawn_worker: %v\n%s", err, out)
	}

	text := string(out)
	for _, want := range []string{
		"status=succeeded",
		"JWT refactor appears complete",
		"--- jwt.go ---",
		"func ValidateJWT",
		"--- auth.go ---",
		"Authentication now uses JWT",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}
