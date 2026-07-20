package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/oauth"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
)

// RunKeys is the entry point for `zarlcode keys ...`.
// It opens the store + vault (auto-generating the master key on
// first use) but skips the TUI, the provider build, and every other
// startup step — the whole point of the subcommand is to populate
// the vault while no provider can yet launch.
//
// Synopsis:
//
//	zarlcode keys                       # alias for `list`
//	zarlcode keys list
//	zarlcode keys set <provider> <key>  # encrypts + stores globally
//	zarlcode keys delete <provider>     # removes the global entry
//	zarlcode keys oauth <provider>      # runs the OAuth flow for a
//	                                       # provider (currently only
//	                                       # openai-codex), persisting
//	                                       # the resulting token bundle
//	                                       # in the same encrypted vault
//	                                       # as plain api keys.
//
// "Globally" means workspace="" in the api_keys table; every
// workspace inherits via the store's built-in fallback.
func RunKeys(args []string, stdout io.Writer) int {
	ctx := context.Background()
	// db.Open creates ~/.zarlcode (MkdirAll) on first use, so we don't
	// pre-seed the full home here — `zarlcode keys` only needs the
	// state.db to exist, not skills/ / tools/ / hooks/ (those are
	// seeded by `zarlcode init` and the implicit TUI first-run).
	store, err := db.Open(ctx, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "store:", err)
		return 1
	}
	defer store.Close()
	// CLI subcommand has no workspace context, so the service is
	// constructed with wsRoot="" — only global-scope operations are
	// valid. Workspace ops would fail with prefs.ErrNoWorkspace anyway, but
	// the explicit empty signals "we don't have a workspace here" to
	// any future caller that might be tempted to add one.
	svc := prefs.NewService(store, nil, "")

	cmd := "list"
	if len(args) > 0 {
		cmd = args[0]
	}
	if needsKeysVault(ctx, svc, cmd) {
		v, err := vault.Open(vault.TerminalPassphrase)
		if err != nil {
			fmt.Fprintln(os.Stderr, "vault:", err)
			return 1
		}
		svc.SetVault(v)
	}
	switch cmd {
	case "list":
		return keysList(ctx, svc, stdout)
	case subcmdSet:
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: zarlcode keys set <provider> <key>")
			return 2
		}
		return keysSet(ctx, svc, args[1], strings.Join(args[2:], " "), stdout)
	case subcmdDelete, "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: zarlcode keys delete <provider>")
			return 2
		}
		return keysDelete(ctx, svc, args[1], stdout)
	case "oauth":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: zarlcode keys oauth <provider>")
			return 2
		}
		return keysOAuth(ctx, svc, args[1], os.Stdin, stdout)
	case "protect":
		return keysProtect(ctx, svc, args[1:], stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q (want list | set | delete | oauth | protect)\n", cmd)
		return 2
	}
}

func needsKeysVault(ctx context.Context, svc *prefs.Service, cmd string) bool {
	switch cmd {
	case "protect":
		return false // the subcommand opens the vault only for on/off as needed.
	case subcmdSet, "oauth":
		mode, err := svc.CredentialProtection(ctx)
		return err == nil && mode == prefs.CredentialProtectionPassphrase
	default:
		return false
	}
}

// keysOAuth runs the interactive OAuth flow for a provider (only
// openai-codex today) and writes the resulting credential through the
// [prefs.Service] at [prefs.ScopeGlobal] — same scope plain api keys use,
// just routed through the shared service so the audit story stays
// uniform.
func keysOAuth(ctx context.Context, svc *prefs.Service, provider string, stdin io.Reader, stdout io.Writer) int {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if err := oauth.RunLogin(ctx, svc, provider, stdin, stdout); err != nil {
		fmt.Fprintln(os.Stderr, "oauth:", err)
		return 1
	}
	return 0
}

func keysList(ctx context.Context, svc *prefs.Service, w io.Writer) int {
	providers, err := svc.ListKeys(ctx, prefs.ScopeGlobal)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list:", err)
		return 1
	}
	if len(providers) == 0 {
		fmt.Fprintln(w, "no api keys stored (try: zarlcode keys set <provider> <key>)")
		return 0
	}
	fmt.Fprintln(w, "stored api keys (global scope):")
	for _, p := range providers {
		fmt.Fprintf(w, "  - %s\n", p)
	}
	return 0
}

func keysSet(ctx context.Context, svc *prefs.Service, provider, key string, w io.Writer) int {
	provider = strings.ToLower(strings.TrimSpace(provider))
	key = strings.TrimSpace(key)
	if provider == "" {
		fmt.Fprintln(os.Stderr, "provider name is empty")
		return 2
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "key is empty")
		return 2
	}
	if err := svc.SetKey(ctx, prefs.ScopeGlobal, provider, key); err != nil {
		fmt.Fprintln(os.Stderr, "set:", err)
		return 1
	}
	fmt.Fprintf(w, "stored api key for %q globally — every workspace inherits via fallback\n", provider)
	return 0
}

func keysDelete(ctx context.Context, svc *prefs.Service, provider string, w io.Writer) int {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if err := svc.DeleteKey(ctx, prefs.ScopeGlobal, provider); err != nil {
		fmt.Fprintln(os.Stderr, "delete:", err)
		return 1
	}
	fmt.Fprintf(w, "removed api key for %q from global scope\n", provider)
	return 0
}

// cmdStatus is the default/"show current state" subcommand shared by the
// keys-protect and upgrade CLI verbs.
const cmdStatus = "status"

func keysProtect(ctx context.Context, svc *prefs.Service, args []string, w io.Writer) int {
	cmd := cmdStatus
	if len(args) > 0 {
		cmd = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch cmd {
	case cmdStatus:
		mode, err := svc.CredentialProtection(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "protect:", err)
			return 1
		}
		fmt.Fprintf(w, "credential protection: %s\n", mode)
		return 0
	case "on", "enable":
		n, err := svc.EnableCredentialProtection(ctx, vault.TerminalPassphrase)
		if err != nil {
			fmt.Fprintln(os.Stderr, "protect on:", err)
			return 1
		}
		fmt.Fprintf(w, "credential protection enabled — encrypted %d key(s)\n", n)
		return 0
	case "off", "disable":
		n, err := svc.DisableCredentialProtection(ctx, vault.TerminalPassphrase)
		if err != nil {
			fmt.Fprintln(os.Stderr, "protect off:", err)
			return 1
		}
		fmt.Fprintf(w, "credential protection off — stored %d key(s) as plaintext\n", n)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: zarlcode keys protect [status|on|off]")
		return 2
	}
}
