package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// callbackAddr is the loopback address the local OAuth callback
// server binds to. This MUST match the redirect_uri the auth flow
// sends — auth.openai.com rejects redirects that don't appear on the
// client's allow-list, and the Codex CLI client is registered for
// http://localhost:1455/auth/callback.
const callbackAddr = "127.0.0.1:1455"

// successHTML is the page shown to the user after the auth
// server completes the redirect. Plain HTML so the browser tab is
// usable on shells without graphical capability. Short on purpose —
// the real signal is that the shell process printed "credential
// stored" back in the terminal.
const successHTML = `<!doctype html>
<html><body style="font-family:sans-serif;text-align:center;padding:2em;">
<h2>You're signed in.</h2>
<p>You can close this tab — zarlcode has captured the credential.</p>
</body></html>`

// RunLogin walks the user through a Codex OAuth flow and stores
// the resulting credential in the vault under CredProvider.
// Returns nil on success, an error on user abort or any failure step.
//
// The flow:
//
//  1. Generate PKCE + state and the auth URL.
//  2. Start a local HTTP listener on callbackAddr. If the bind
//     fails (port already in use), fall through to manual mode.
//  3. Open the browser to the URL (best-effort; non-blocking).
//  4. Wait for the callback OR (manual mode) prompt for the user to
//     paste the redirect URL.
//  5. Exchange the code for tokens; persist via [prefs.Service] at
//     [prefs.ScopeGlobal] so every workspace inherits the credential.
func RunLogin(
	ctx context.Context,
	svc *prefs.Service,
	stdin io.Reader,
	stdout io.Writer,
) error {
	flow, err := openaicodex.CreateAuthorizationFlow()
	if err != nil {
		return fmt.Errorf("oauth: build authorization flow: %w", err)
	}

	listener, listenErr := (&net.ListenConfig{}).Listen(ctx, "tcp", callbackAddr)
	if listenErr != nil {
		fmt.Fprintf(
			stdout,
			"oauth: could not bind %s (%v); falling back to manual paste\n",
			callbackAddr,
			listenErr,
		)
		return runManual(ctx, svc, flow, stdin, stdout)
	}
	defer listener.Close()

	codeCh := make(chan callbackResult, 1)
	srv := zhttp.NewServer("", makeCallbackHandler(flow.State, codeCh))
	go func() {
		_ = srv.Serve(listener)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), serverShutdown)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Fprintln(stdout, "oauth: opening browser for ChatGPT sign-in...")
	fmt.Fprintln(stdout, "  if it doesn't open automatically, visit:")
	fmt.Fprintln(stdout, "  "+flow.URL)
	openBrowser(ctx, flow.URL) // best-effort

	select {
	case <-ctx.Done():
		return ctx.Err()
	case res := <-codeCh:
		if res.err != nil {
			return fmt.Errorf("oauth: callback: %w", res.err)
		}
		return finishLogin(ctx, svc, flow.PKCE.Verifier, res.code, stdout)
	}
}

// runManual is the fallback path when the local callback
// listener can't bind. Prompts the user to paste the redirect URL (or
// just the code) from their browser. Mirrors the auto-flow's UX
// otherwise.
func runManual(
	ctx context.Context,
	svc *prefs.Service,
	flow openaicodex.AuthorizationFlow,
	stdin io.Reader,
	stdout io.Writer,
) error {
	fmt.Fprintln(stdout, "oauth: visit this URL in your browser:")
	fmt.Fprintln(stdout, "  "+flow.URL)
	openBrowser(ctx, flow.URL)
	fmt.Fprint(stdout, "oauth: paste the redirect URL (or code) here: ")
	buf := make([]byte, 0, 2048)
	tmp := make([]byte, 256)
	for {
		n, err := stdin.Read(tmp)
		if n > 0 {
			// Stop at newline — keep everything before it.
			if i := indexByte(tmp[:n], '\n'); i >= 0 {
				buf = append(buf, tmp[:i]...)
				break
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("oauth: read input: %w", err)
		}
	}
	code, state := openaicodex.ParseAuthorizationInput(strings.TrimSpace(string(buf)))
	if code == "" {
		return errors.New("oauth: no authorization code in input")
	}
	if state == "" {
		return errors.New("oauth: redirect URL with state required — raw authorization codes are refused")
	}
	if state != flow.State {
		return errors.New("oauth: state mismatch — refusing to continue")
	}
	return finishLogin(ctx, svc, flow.PKCE.Verifier, code, stdout)
}

// indexByte is strings.IndexByte for byte slices — local to keep the
// import footprint of this file small.
func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// finishLogin trades the authorization code for tokens and
// persists them at [prefs.ScopeGlobal] via the [prefs.Service] so every
// workspace inherits the credential through the standard precedence
// chain. Shared by both the auto and manual flow paths.
func finishLogin(ctx context.Context, svc *prefs.Service, verifier, code string, stdout io.Writer) error {
	tok, err := openaicodex.ExchangeAuthorizationCode(ctx, code, verifier)
	if err != nil {
		return fmt.Errorf("oauth: exchange code: %w", err)
	}
	cred := credFromToken(tok)
	raw, err := json.Marshal(cred)
	if err != nil {
		return fmt.Errorf("oauth: encode credential: %w", err)
	}
	if err := svc.SetKey(ctx, prefs.ScopeGlobal, CredProvider, string(raw)); err != nil {
		return fmt.Errorf("oauth: persist credential: %w", err)
	}
	fmt.Fprintf(stdout, "oauth: stored ChatGPT credential globally (account %s)\n", tok.AccountID)
	return nil
}

// callbackResult is what the in-process listener hands back —
// either a code on success or an err describing the failure.
type callbackResult struct {
	code string
	err  error
}

// makeCallbackHandler returns an http.HandlerFunc that listens at
// /auth/callback, validates the state param against the expected
// value, and pushes the code into resCh exactly once. Bad-state
// callbacks return 400 but do not unblock the waiter: a stray local
// request should not be able to deny the real browser redirect.
// Missing-code callbacks still unblock with an error because a request
// with the right state means the OAuth provider reached us but did not
// provide the required credential.
func makeCallbackHandler(expectedState string, resCh chan<- callbackResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("state")
		code := r.URL.Query().Get("code")
		if got != expectedState {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		if code == "" {
			http.Error(w, "missing authorization code", http.StatusBadRequest)
			select {
			case resCh <- callbackResult{err: errors.New("missing authorization code")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, successHTML)
		select {
		case resCh <- callbackResult{code: code}:
		default:
		}
	})
	return mux
}

// openBrowser tries to open the given URL in the user's default
// browser. Failures are silent — the caller has already printed the
// URL for the user to copy/paste as a fallback. It's a var so tests
// driving runManual/RunLogin can stub it: running the real one would
// spawn a ChatGPT login tab on every `go test` of this package.
var openBrowser = func(ctx context.Context, url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	}
	_ = cmd.Start()
}
