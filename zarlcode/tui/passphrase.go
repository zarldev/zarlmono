package tui

import (
	"context"
	"os"

	"github.com/zarldev/zarlmono/zkit/vault"
	"golang.org/x/term"
)

// vaultPassphraseFunc returns the interactive passphrase prompt only when a
// vault already exists on disk and startup prompting is enabled. A fresh install
// with no stored credentials (the local-llama.cpp default) is never prompted and
// runs with the vault disabled until the user initialises it via `zarlcode keys
// set`. Returning nil means "don't prompt" — Open then degrades to
// keys-disabled (unless $ZARLCODE_KEY / $ZARLCODE_PASSPHRASE is set, which Open
// honours regardless).
func vaultPassphraseFunc(ctx context.Context, splash bool) vault.PassphraseFunc {
	if splash && term.IsTerminal(int(os.Stdin.Fd())) {
		return func(setup, retry bool) (string, error) {
			return runVaultUnlockSplash(ctx, setup, retry)
		}
	}
	return vault.TerminalPassphrase
}
