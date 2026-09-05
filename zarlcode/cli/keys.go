package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/oauth"
	"github.com/zarldev/zarlmono/zkit/vault"
)

// OAuthLoginFunc dispatches an interactive OAuth login for a provider.
type OAuthLoginFunc func(context.Context, *prefs.Service, string, io.Reader, io.Writer) error

// KeysCommand executes key-management commands against an explicitly supplied
// preference service. Stdin and OAuthLogin are optional environment overrides;
// their zero values select the production process input and OAuth dispatcher.
type KeysCommand struct {
	Service    *prefs.Service
	Stdin      io.Reader
	OAuthLogin OAuthLoginFunc
}

// Execute runs a keys command and returns its process exit code.
func (c KeysCommand) Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	stdin := c.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	oauthLogin := c.OAuthLogin
	if oauthLogin == nil {
		oauthLogin = func(ctx context.Context, svc *prefs.Service, provider string, stdin io.Reader, stdout io.Writer) error {
			return oauth.RunLogin(ctx, svc, provider, stdin, stdout)
		}
	}

	cmd := cmdList
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case cmdList:
		return keysList(ctx, c.Service, stdout, stderr)
	case subcmdSet:
		if len(args) < 3 {
			fmt.Fprintln(stderr, "usage: zarlcode keys set <provider> <key>")
			return 2
		}
		return keysSet(ctx, c.Service, args[1], strings.Join(args[2:], " "), stdout, stderr)
	case subcmdDelete, "rm":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: zarlcode keys delete <provider>")
			return 2
		}
		return keysDelete(ctx, c.Service, args[1], stdout, stderr)
	case "oauth":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: zarlcode keys oauth <provider>")
			return 2
		}
		return keysOAuth(ctx, c.Service, oauthLogin, args[1], stdin, stdout, stderr)
	case "protect":
		return keysProtect(ctx, c.Service, "", args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q (want list | set | delete | oauth | protect)\n", cmd)
		return 2
	}
}

// RunKeys is the entry point for `zarlcode keys ...`. It opens only the
// preference store and any vault required by the requested operation.
func RunKeys(args []string, stdout io.Writer) int {
	ctx := context.Background()
	dir, err := db.DefaultDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "state directory:", err)
		return 1
	}
	store, err := db.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "store:", err)
		return 1
	}
	defer store.Close()
	svc := prefs.NewService(store, nil, "")

	cmd := cmdList
	if len(args) > 0 {
		cmd = args[0]
	}
	if needsKeysVault(ctx, svc, cmd) {
		v, err := vault.Open(dir, vault.TerminalPassphrase)
		if err != nil {
			fmt.Fprintln(os.Stderr, "vault:", err)
			return 1
		}
		svc.SetVault(v)
	}
	return (KeysCommand{Service: svc}).Execute(ctx, args, stdout, os.Stderr)
}

func needsKeysVault(ctx context.Context, svc *prefs.Service, cmd string) bool {
	switch cmd {
	case "protect":
		return false
	case subcmdSet, "oauth":
		mode, err := svc.CredentialProtection(ctx)
		return err == nil && mode == prefs.CredentialProtectionPassphrase
	default:
		return false
	}
}

func keysOAuth(ctx context.Context, svc *prefs.Service, login OAuthLoginFunc, provider string, stdin io.Reader, stdout, stderr io.Writer) int {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		fmt.Fprintln(stderr, "provider name is empty")
		return 2
	}
	if err := login(ctx, svc, provider, stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "oauth:", err)
		return 1
	}
	return 0
}

func keysList(ctx context.Context, svc *prefs.Service, stdout, stderr io.Writer) int {
	providers, err := svc.ListKeys(ctx, prefs.ScopeGlobal)
	if err != nil {
		fmt.Fprintln(stderr, "list:", err)
		return 1
	}
	if len(providers) == 0 {
		fmt.Fprintln(stdout, "no api keys stored (try: zarlcode keys set <provider> <key>)")
		return 0
	}
	fmt.Fprintln(stdout, "stored api keys (global scope):")
	for _, provider := range providers {
		fmt.Fprintf(stdout, "  - %s\n", provider)
	}
	return 0
}

func keysSet(ctx context.Context, svc *prefs.Service, provider, key string, stdout, stderr io.Writer) int {
	provider = strings.ToLower(strings.TrimSpace(provider))
	key = strings.TrimSpace(key)
	if provider == "" {
		fmt.Fprintln(stderr, "provider name is empty")
		return 2
	}
	if key == "" {
		fmt.Fprintln(stderr, "key is empty")
		return 2
	}
	if err := svc.SetKey(ctx, prefs.ScopeGlobal, provider, key); err != nil {
		fmt.Fprintln(stderr, "set:", err)
		return 1
	}
	fmt.Fprintf(stdout, "stored api key for %q globally — every workspace inherits via fallback\n", provider)
	return 0
}

func keysDelete(ctx context.Context, svc *prefs.Service, provider string, stdout, stderr io.Writer) int {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		fmt.Fprintln(stderr, "provider name is empty")
		return 2
	}
	if err := svc.DeleteKey(ctx, prefs.ScopeGlobal, provider); err != nil {
		fmt.Fprintln(stderr, "delete:", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed api key for %q from global scope\n", provider)
	return 0
}

// cmdStatus is the default/"show current state" subcommand shared by the
// keys-protect and upgrade CLI verbs.
const (
	cmdList   = "list"
	cmdStatus = "status"
)

func keysProtect(ctx context.Context, svc *prefs.Service, dir string, args []string, stdout, stderr io.Writer) int {
	cmd := cmdStatus
	if len(args) > 0 {
		cmd = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch cmd {
	case cmdStatus:
		mode, err := svc.CredentialProtection(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "protect:", err)
			return 1
		}
		fmt.Fprintf(stdout, "credential protection: %s\n", mode)
		return 0
	case "on", "enable":
		n, err := changeCredentialProtection(ctx, svc, dir, true)
		if err != nil {
			fmt.Fprintln(stderr, "protect on:", err)
			return 1
		}
		fmt.Fprintf(stdout, "credential protection enabled — encrypted %d key(s)\n", n)
		return 0
	case "off", "disable":
		n, err := changeCredentialProtection(ctx, svc, dir, false)
		if err != nil {
			fmt.Fprintln(stderr, "protect off:", err)
			return 1
		}
		fmt.Fprintf(stdout, "credential protection off — stored %d key(s) as plaintext\n", n)
		return 0
	default:
		fmt.Fprintln(stderr, "usage: zarlcode keys protect [status|on|off]")
		return 2
	}
}

func changeCredentialProtection(ctx context.Context, svc *prefs.Service, dir string, enabled bool) (int, error) {
	hasRows, err := svc.HasVaultBackedKeys(ctx)
	if err != nil {
		return 0, err
	}
	if (enabled || hasRows) && !svc.HasVault() && dir != "" {
		v, err := vault.Open(dir, vault.TerminalPassphrase)
		if err != nil {
			return 0, err
		}
		svc.SetVault(v)
	}
	if enabled {
		return svc.EnableCredentialProtection(ctx)
	}
	return svc.DisableCredentialProtection(ctx)
}
