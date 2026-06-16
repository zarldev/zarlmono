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
// Returns an error when stdin isn't a terminal so the caller degrades
// gracefully (set $ZARLCODE_PASSPHRASE for non-interactive use) rather than
// blocking on a read that can never succeed.
func TerminalPassphrase(setup, retry bool) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("vault passphrase required but stdin is not a terminal (set ZARLCODE_PASSPHRASE)")
	}
	if retry {
		fmt.Fprintln(os.Stderr, "Passphrase incorrect — try again.")
	}
	if setup {
		fmt.Fprintln(os.Stderr, "Set a passphrase to encrypt zarlcode credentials at rest.")
		fmt.Fprintln(os.Stderr, "(Set ZARLCODE_PASSPHRASE to skip this prompt on future launches.)")
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
	return readSecret(fd, "zarlcode vault passphrase: ")
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
