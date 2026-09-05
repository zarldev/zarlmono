package vault

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// TerminalPassphrase is the default interactive [PassphraseFunc]: it reads the
// vault passphrase from the controlling terminal with echo disabled. Intended
// to run BEFORE any alt-screen TUI starts (it's a plain stdin read, not a
// bubbletea overlay). On first-ever setup it asks for a confirmation; on a
// returning launch it asks once and Open retries on a wrong passphrase.
//
// Returns an error when stdin is not a terminal rather than blocking on an
// unavailable input source. Non-interactive callers must supply their own
// PassphraseFunc explicitly, or leave protected credentials locked.
func TerminalPassphrase(setup, retry bool) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("vault passphrase required but stdin is not a terminal")
	}
	if retry {
		fmt.Fprintln(os.Stderr, "Passphrase incorrect — try again.")
	}
	if setup {
		fmt.Fprintln(os.Stderr, "Set a passphrase to encrypt credentials at rest.")
		pass, err := readSecret(fd, "New passphrase: ")
		if err != nil {
			return "", err
		}
		if pass == "" {
			return "", errors.New("empty passphrase")
		}
		confirm, err := readSecret(fd, "Confirm passphrase: ")
		if err != nil {
			return "", err
		}
		if pass != confirm {
			return "", errors.New("passphrases did not match")
		}
		return pass, nil
	}
	return readSecret(fd, "Vault passphrase: ")
}

// readSecret prints label to stderr and reads a line from the terminal without
// echoing it, then emits the newline term.ReadPassword swallows.
func readSecret(fd int, label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}
