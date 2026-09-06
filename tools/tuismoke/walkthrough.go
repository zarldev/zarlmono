package tuismoke

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

const keyEnter = "Enter"

func (h *harness) walkthrough(ctx context.Context) error {
	if err := h.start(ctx, "smoke"); err != nil {
		return err
	}
	if _, err := h.wait(ctx, "first-run setup"); err != nil {
		return err
	}
	steps := []struct {
		text string
		keys []string
	}{
		{"build mode", []string{keyEnter}},
		{"settings · model", []string{"C-s"}},
		{"key/sign-in", []string{"Down", keyEnter}},
		{"vault unlock", []string{keyEnter, "smoke-secret", keyEnter}},
		{"confirm", []string{passphrase, keyEnter}},
		{"openai key saved (global)", []string{passphrase, keyEnter}},
		// Wait between Escapes; a burst can decode as Alt+Escape.
		{"esc done", []string{"Escape"}},
		{"build mode", []string{"Escape"}},
	}
	for _, step := range steps {
		if err := h.step(ctx, step.text, step.keys...); err != nil {
			return err
		}
	}
	if err := VerifyCredentialStorage(ctx, filepath.Join(h.dir, "home", ".zarlcode", "state.db")); err != nil {
		return err
	}
	if _, err := h.command(ctx, "resize-window", "-t", h.session, "-x", "60", "-y", "16"); err != nil {
		return err
	}
	if err := h.step(ctx, "[keys]", "C-g"); err != nil {
		return err
	}
	if _, err := h.wait(ctx, "contextual shortcuts"); err != nil {
		return err
	}
	if err := h.step(ctx, "build mode", "Escape"); err != nil {
		return err
	}
	if err := h.quit(ctx); err != nil {
		return err
	}
	if err := h.start(ctx, "unlock"); err != nil {
		return err
	}
	if _, err := h.wait(ctx, "vault unlock"); err != nil {
		return err
	}
	if err := h.keys(ctx, passphrase, keyEnter); err != nil {
		return err
	}
	screen, err := h.wait(ctx, "ctrl+g keys")
	if err != nil {
		return err
	}
	if strings.Contains(screen, "setup required") || strings.Contains(screen, "first-run setup") {
		return errors.New("onboarding repeated after accepting local defaults")
	}
	return h.quit(ctx)
}
