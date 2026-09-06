package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestOAuthEscapeCancelsOwnedAttempt(t *testing.T) {
	harness := tui.NewOAuthBehaviorHarness(t.Context(), newTestSettings(t))
	cancelled := 0
	id := harness.StartAttempt("openai-codex", context.CancelFunc(func() { cancelled++ }))
	if id == 0 || !harness.Busy() || !harness.InSubMode() {
		t.Fatalf("attempt=%d busy=%v submode=%v", id, harness.Busy(), harness.InSubMode())
	}
	if footer := harness.Footer(); !strings.Contains(footer, "esc") || !strings.Contains(footer, "cancel") {
		t.Fatalf("footer=%q, want real Esc cancellation hint", footer)
	}

	harness.PressSettingsEscape()

	if cancelled != 1 {
		t.Fatalf("cancel calls=%d, want 1", cancelled)
	}
	if harness.Busy() || harness.ActiveAttempt() != 0 {
		t.Fatalf("busy=%v active=%d after cancel", harness.Busy(), harness.ActiveAttempt())
	}
	if status := harness.Status(); status != "sign-in cancelled" {
		t.Fatalf("status=%q", status)
	}
}

func TestOAuthSupersedeAndStaleResultsCannotMutateCurrentAttempt(t *testing.T) {
	harness := tui.NewOAuthBehaviorHarness(t.Context(), newTestSettings(t))
	firstCancelled := 0
	secondCancelled := 0
	first := harness.StartAttempt("openai-codex", context.CancelFunc(func() { firstCancelled++ }))
	second := harness.StartAttempt("openai-codex", context.CancelFunc(func() { secondCancelled++ }))
	if firstCancelled != 1 {
		t.Fatalf("superseded cancel calls=%d, want 1", firstCancelled)
	}
	if harness.ActiveAttempt() != second || !harness.Busy() {
		t.Fatalf("active=%d busy=%v, want second=%d", harness.ActiveAttempt(), harness.Busy(), second)
	}

	harness.ApplySuccess(first, "openai-codex", "stale-account")
	harness.ApplyFailure(first, "openai-codex", errors.New("stale failure"))
	if harness.ActiveAttempt() != second || !harness.Busy() {
		t.Fatalf("stale result changed active=%d busy=%v", harness.ActiveAttempt(), harness.Busy())
	}
	if status := harness.Status(); strings.Contains(status, "stale") {
		t.Fatalf("stale result changed status=%q", status)
	}

	harness.ApplySuccess(second, "openai-codex", "current-account")
	if secondCancelled != 1 {
		t.Fatalf("completed attempt cleanup calls=%d, want 1", secondCancelled)
	}
	if harness.ActiveAttempt() != 0 || harness.Busy() {
		t.Fatalf("active=%d busy=%v after completion", harness.ActiveAttempt(), harness.Busy())
	}
	if status := harness.Status(); !strings.Contains(status, "current-account") {
		t.Fatalf("status=%q, want current account", status)
	}
}

func TestOAuthOwnerCloseCancelsAndRejectsLateResult(t *testing.T) {
	harness := tui.NewOAuthBehaviorHarness(t.Context(), newTestSettings(t))
	cancelled := 0
	id := harness.StartAttempt("openai-codex", context.CancelFunc(func() { cancelled++ }))

	harness.CloseSettings()
	harness.ApplySuccess(id, "openai-codex", "late-account")

	if cancelled != 1 || harness.ActiveAttempt() != 0 {
		t.Fatalf("cancel calls=%d active=%d", cancelled, harness.ActiveAttempt())
	}
	if status := harness.Status(); strings.Contains(status, "late-account") {
		t.Fatalf("late result changed dismissed owner status=%q", status)
	}
}

func TestOAuthCtrlCUsesRootConfirmationThenCancelsOnQuit(t *testing.T) {
	harness := tui.NewOAuthBehaviorHarness(t.Context(), newTestSettings(t))
	harness.SetConfirmQuit(true)
	cancelled := 0
	id := harness.StartAttempt("openai-codex", context.CancelFunc(func() { cancelled++ }))
	status := harness.Status()

	if cmd := harness.UpdateKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd != nil {
		t.Fatal("initial Ctrl+C returned a command before confirmation")
	}
	if !harness.QuitConfirmationOpen() {
		t.Fatal("Ctrl+C did not open the root quit confirmation above settings")
	}
	if cancelled != 0 || harness.ActiveAttempt() != id {
		t.Fatalf("attempt changed before confirmation: cancel calls=%d active=%d", cancelled, harness.ActiveAttempt())
	}

	cmd := harness.UpdateKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirmed quit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("confirmed quit command did not emit tea.QuitMsg")
	}
	if cancelled != 1 || harness.ActiveAttempt() != 0 {
		t.Fatalf("confirmed quit cancel calls=%d active=%d, want 1 and 0", cancelled, harness.ActiveAttempt())
	}

	harness.ApplySuccessThroughUpdate(id, "openai-codex", "late-account")
	if cancelled != 1 || harness.ActiveAttempt() != 0 {
		t.Fatalf("late result changed retired attempt: cancel calls=%d active=%d", cancelled, harness.ActiveAttempt())
	}
	if got := harness.Status(); got != status {
		t.Fatalf("late result changed owner status from %q to %q", status, got)
	}
}

func TestClaudeOAuthFooterDoesNotAdvertiseTUICancellation(t *testing.T) {
	harness := tui.NewOAuthBehaviorHarness(t.Context(), newTestSettings(t))
	harness.SelectProvider("claude-code")
	footer := harness.Footer()
	if strings.Contains(footer, "esc cancel") {
		t.Fatalf("Claude footer falsely advertises TUI cancellation: %q", footer)
	}
}
