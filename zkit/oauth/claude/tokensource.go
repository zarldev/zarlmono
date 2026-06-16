package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/claudecode"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// CredProvider is the canonical provider key under which Claude Code
// OAuth credentials are stored in the api_keys table — the same row
// space as plain API keys, so `zarlcode keys list` surfaces it for free.
const CredProvider = "claude-code"

type cred struct {
	Access      string `json:"access"`
	ExpiresUnix int64  `json:"expires_unix,omitempty"`
}

func (c cred) toToken() claudecode.Token {
	var expires time.Time
	if c.ExpiresUnix != 0 {
		expires = time.Unix(c.ExpiresUnix, 0)
	}
	return claudecode.Token{Access: c.Access, Expires: expires}
}

func credFromToken(t claudecode.Token) cred {
	c := cred{Access: t.Access}
	if !t.Expires.IsZero() {
		c.ExpiresUnix = t.Expires.Unix()
	}
	return c
}

type tokenSource struct {
	svc *prefs.Service
	mu  sync.Mutex
}

// NewTokenSource returns a claudecode.TokenSource backed by the
// prefs vault: each Token read loads the stored credential under a lock,
// so a token refreshed by a login flow is picked up without restarting
// the consumer.
func NewTokenSource(svc *prefs.Service) *tokenSource {
	return &tokenSource{svc: svc}
}

func (s *tokenSource) Token(ctx context.Context) (claudecode.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cred, ok, err := s.readCred(ctx)
	if err != nil {
		return claudecode.Token{}, err
	}
	if !ok {
		return claudecode.Token{}, errors.New(
			"claudecode: not signed in — sign in to Claude Code first",
		)
	}
	tok := cred.toToken()
	if tok.Access == "" {
		return claudecode.Token{}, errors.New("claudecode: stored credential has no access token — sign in again")
	}
	if !tok.Expires.IsZero() && time.Now().After(tok.Expires) {
		return claudecode.Token{}, errors.New(
			"claudecode: stored OAuth token is expired — sign in to Claude Code again",
		)
	}
	return tok, nil
}

func (s *tokenSource) readCred(ctx context.Context) (cred, bool, error) {
	kv, ok, err := s.svc.GetKeyEffective(ctx, CredProvider)
	if err != nil || !ok {
		return cred{}, ok, err
	}
	var c cred
	if err := json.Unmarshal([]byte(kv.Value), &c); err != nil {
		return cred{}, false, fmt.Errorf(
			"claudecode: stored credential is not valid JSON — sign in to Claude Code again: %w", err)
	}
	return c, true, nil
}
