package main_test

import (
	"os/exec"
	"testing"
)

func TestNotifyDrainCLI(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run notify_drain: %v\n%s", err, out)
	}
	const want = "live=\"delivered live\"\n" +
		"subscription_closed=true\n" +
		"drained=1 content=\"queued while offline\"\n" +
		"drained_again=0\n"
	if string(out) != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}
