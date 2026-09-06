package tui_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestCodexLoginOwnsCancellableAttemptBeforeCommandRuns(t *testing.T) {
	harness := tui.NewOAuthBehaviorHarness(t.Context(), newTestSettings(t))
	cmd := harness.StartLogin("openai-codex")
	if cmd == nil {
		t.Fatal("Codex login returned no callback command")
	}
	if harness.ActiveAttempt() == 0 || !harness.Busy() || !harness.InSubMode() {
		t.Fatalf("active=%d busy=%v submode=%v", harness.ActiveAttempt(), harness.Busy(), harness.InSubMode())
	}

	harness.PressSettingsEscape()
	if harness.ActiveAttempt() != 0 || harness.Busy() {
		t.Fatalf("active=%d busy=%v after Esc", harness.ActiveAttempt(), harness.Busy())
	}
}
