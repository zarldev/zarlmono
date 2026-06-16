package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// serverShutdown is the grace period for the OAuth callback HTTP
// server to drain before force-close.
const serverShutdown = 2 * time.Second

// AwaitCallback binds the loopback OAuth callback listener, waits
// for the browser redirect (bounded by ctx), exchanges the code, and
// persists the resulting credential at [prefs.ScopeGlobal]. The caller
// is responsible for having already shown flow.URL to the user — the
// TUI prints it into the transcript first. Returns the ChatGPT account
// id on success.
//
// This is the headless sibling of [RunLogin]'s auto path: no stdin, no
// browser-open, no manual fallback — just bind, wait, exchange, store —
// because the bubbletea shell drives the flow as a background tea.Cmd
// and has no terminal to prompt on.
func AwaitCallback(ctx context.Context, svc *prefs.Service, flow openaicodex.AuthorizationFlow) (string, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", callbackAddr)
	if err != nil {
		return "", fmt.Errorf(
			"bind %s: %w — port already in use; close any other oauth flow and retry, "+
				"or use the CLI fallback: `zarlcode keys oauth openai-codex`",
			callbackAddr, err)
	}
	defer listener.Close()

	codeCh := make(chan callbackResult, 1)
	srv := zhttp.NewServer("", makeCallbackHandler(flow.State, codeCh))
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), serverShutdown)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	var code string
	select {
	case <-ctx.Done():
		return "", errors.New("oauth: timed out waiting for browser callback")
	case res := <-codeCh:
		if res.err != nil {
			return "", fmt.Errorf("callback: %w", res.err)
		}
		code = res.code
	}

	tok, err := openaicodex.ExchangeAuthorizationCode(ctx, code, flow.PKCE.Verifier)
	if err != nil {
		return "", fmt.Errorf("exchange code: %w", err)
	}
	raw, err := json.Marshal(credFromToken(tok))
	if err != nil {
		return "", fmt.Errorf("encode credential: %w", err)
	}
	if err := svc.SetKey(ctx, prefs.ScopeGlobal, CredProvider, string(raw)); err != nil {
		return "", fmt.Errorf("persist credential: %w", err)
	}
	return tok.AccountID, nil
}
