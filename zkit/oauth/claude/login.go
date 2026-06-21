package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// RunLogin walks the user through the Claude Code sign-in: it runs
// `claude setup-token` attached to stdin/stdout, extracts the printed
// OAuth token from the output (falling back to a manual paste prompt
// when the subprocess fails), and persists it at [prefs.ScopeGlobal]
// under CredProvider.
func RunLogin(ctx context.Context, svc *prefs.Service, stdin io.Reader, stdout io.Writer) error {
	fmt.Fprintln(stdout, "oauth: launching `claude setup-token` for Claude Code sign-in...")
	fmt.Fprintln(stdout, "oauth: complete the browser flow, then paste the printed token here if prompted.")
	cmd := exec.CommandContext(ctx, "claude", "setup-token")
	cmd.Stdin = stdin
	cmd.Stderr = stdout
	var captured bytes.Buffer
	cmd.Stdout = io.MultiWriter(stdout, &captured)
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(stdout, "oauth: `claude setup-token` did not return a token directly.")
		fmt.Fprint(stdout, "oauth: paste the CLAUDE_CODE_OAUTH_TOKEN value here: ")
		line, readErr := bufio.NewReader(stdin).ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return fmt.Errorf("oauth: read token: %w", readErr)
		}
		return finishLogin(ctx, svc, strings.TrimSpace(line), stdout)
	}
	return finishLogin(ctx, svc, extractToken(captured.String()), stdout)
}

func extractToken(out string) string {
	var candidates []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if key, val, ok := strings.Cut(line, "="); ok {
			_ = key
			if tok := normalizeCandidate(val); looksLikeClaudeOAuthToken(tok) {
				if strings.HasPrefix(tok, "sk-") {
					return tok
				}
				candidates = append(candidates, tok)
				continue
			}
		}
		for _, field := range strings.FieldsFunc(line, func(r rune) bool {
			switch r {
			case ' ', '\t', '\r', '\n', '\'', '"', '=', ':', ',', ';', '(', ')', '[', ']', '{', '}':
				return true
			default:
				return false
			}
		}) {
			tok := normalizeCandidate(field)
			if strings.EqualFold(tok, "CLAUDE_CODE_OAUTH_TOKEN") {
				continue
			}
			if !looksLikeClaudeOAuthToken(tok) {
				continue
			}
			if strings.HasPrefix(tok, "sk-") {
				return tok
			}
			candidates = append(candidates, tok)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

func normalizeCandidate(s string) string {
	return strings.TrimSpace(strings.Trim(s, "'\""))
}

func looksLikeClaudeOAuthToken(s string) bool {
	s = strings.TrimSpace(strings.Trim(s, "'\""))
	if len(s) < 20 {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '_', '-', '.':
			continue
		default:
			return false
		}
	}
	return true
}

func finishLogin(ctx context.Context, svc *prefs.Service, token string, stdout io.Writer) error {
	if token == "" {
		return errors.New("oauth: no Claude Code OAuth token provided")
	}
	cred := credFromToken(claudecode.Token{Access: token})
	raw, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("oauth: encode credential: %w", err)
	}
	if err := svc.SetKey(ctx, prefs.ScopeGlobal, CredProvider, string(raw)); err != nil {
		return fmt.Errorf("oauth: persist credential: %w", err)
	}
	fmt.Fprintln(stdout, "oauth: stored Claude Code OAuth credential globally")
	return nil
}

// SetupTokenCommand returns the `claude setup-token` command that runs
// Claude Code's browser sign-in and prints an OAuth token. A front-end runs
// it attached to the terminal (e.g. bubbletea's Exec, which suspends the
// alt-screen) and feeds the captured output to StoreToken — so the
// user signs in from the app, with no manual CLI step.
func SetupTokenCommand() *exec.Cmd {
	return exec.CommandContext(context.Background(), "claude", "setup-token")
}

// StoreToken extracts the OAuth token from `claude setup-token`
// output (or accepts a bare pasted token) and persists it at global scope.
func StoreToken(ctx context.Context, svc *prefs.Service, output string) error {
	return finishLogin(ctx, svc, extractToken(output), io.Discard)
}
