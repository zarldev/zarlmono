package tui

import (
	"context"
	"os"

	"golang.org/x/term"

	"github.com/zarldev/zarlmono/zkit/vault"
)

// vaultPassphraseFunc selects the startup unlock UI. OpenSettings decides
// whether to invoke it from database policy and credential rows. Headless mode
// passes splash=false and receives nil, so it never prompts or reads ambient
// credential variables; protected credentials simply remain locked.
func vaultPassphraseFunc(ctx context.Context, splash bool) vault.PassphraseFunc {
	if !splash {
		return nil
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return func(setup, retry bool) (string, error) {
			return runVaultUnlockSplash(ctx, setup, retry)
		}
	}
	return vault.TerminalPassphrase
}
