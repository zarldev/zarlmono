package tuismoke

import (
	"context"
	"errors"
	"runtime"
	"strings"
)

const keyEnter = "Enter"

func (h *harness) walkthrough(ctx context.Context) error {
	if runtime.GOOS == "darwin" {
		if err := h.start(ctx, "sandbox-decline"); err != nil {
			return err
		}
		if _, err := h.wait(ctx, "remember for this workspace"); err != nil {
			return err
		}
		if err := h.keys(ctx, keyEnter); err != nil {
			return err
		}
		if err := h.waitForShutdown(ctx); err != nil {
			return err
		}
	}
	if err := h.start(ctx, "smoke"); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		if _, err := h.wait(ctx, "remember for this workspace"); err != nil {
			return err
		}
		if err := h.keys(ctx, "y"); err != nil {
			return err
		}
	}
	if _, err := h.wait(ctx, "first-run setup"); err != nil {
		return err
	}
	steps := []struct {
		text string
		keys []string
	}{
		{"build mode", []string{keyEnter}},
		{"[settings]", []string{"C-s"}},
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
	if err := h.step(ctx, "[settings]", "C-s"); err != nil {
		return err
	}
	if err := h.step(ctx, "key/sign-in", "Down", keyEnter); err != nil {
		return err
	}
	screen, err = h.wait(ctx, "key set")
	if err != nil {
		return err
	}
	credentialRestored := false
	for line := range strings.Lines(screen) {
		credentialRestored = credentialRestored || strings.Contains(line, "openai") && strings.Contains(line, "key set")
	}
	if !credentialRestored {
		return errors.New("saved OpenAI credential was not visible after restart")
	}
	if err := h.step(ctx, "What are we building?", "C-s"); err != nil {
		return err
	}
	if err := h.quit(ctx); err != nil {
		return err
	}
	if err := h.start(ctx, "cancel"); err != nil {
		return err
	}
	screen, err = h.wait(ctx, "vault unlock")
	if err != nil {
		return err
	}
	if !strings.Contains(screen, "esc exit") || !strings.Contains(screen, "ctrl+c exit") {
		return errors.New("startup vault prompt does not advertise clean exit controls")
	}
	if strings.Contains(screen, "skip vault") {
		return errors.New("startup vault prompt still advertises skipping locked credentials")
	}
	if err := h.keys(ctx, "Escape"); err != nil {
		return err
	}
	return h.waitForShutdown(ctx)
}
