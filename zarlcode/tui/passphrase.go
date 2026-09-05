package tui

import (
	"context"
	"os"

	"github.com/zarldev/zarlmono/zkit/vault"
	"golang.org/x/term"
)

// vaultPassphraseFunc selects the startup unlock UI. OpenSettings decides
// whether to invoke it from database settings and encrypted credential rows.
// Fresh plaintext installations do not prompt; no environment override can
// unlock credentials or change the configured protection mode.
func vaultPassphraseFunc(ctx context.Context, splash bool) vault.PassphraseFunc {
	if splash && term.IsTerminal(int(os.Stdin.Fd())) {
		return func(setup, retry bool) (string, error) {
			return runVaultUnlockSplash(ctx, setup, retry)
		}
	}
	return vault.TerminalPassphrase
}
