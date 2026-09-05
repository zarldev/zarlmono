package codex_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
)

func TestTokenSourceReturnsFreshStoredToken(t *testing.T) {
	svc := openTestService(t)
	jwt := makeJWT(t, "acct_fresh")
	storeCred(t, svc, codex.Cred{
		Access: jwt, Refresh: "refresh-1",
		ExpiresUnix: time.Now().Add(time.Hour).Unix(), AccountID: "acct_fresh",
	})

	src := codex.NewTokenSource(svc, codex.WithTokenRefresh(func(context.Context, string) (openaicodex.Token, error) {
		t.Fatal("unexpected token refresh")
		return openaicodex.Token{}, nil
	}))
	tok, err := src.Token(t.Context())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok.Access != jwt || tok.AccountID != "acct_fresh" {
		t.Errorf("Token() = %+v, want stored token", tok)
	}
}

func TestTokenSourceRefreshesAndPersistsExpiredToken(t *testing.T) {
	svc := openTestService(t)
	storeCred(t, svc, codex.Cred{
		Access: "old", Refresh: "refresh-old",
		ExpiresUnix: time.Now().Add(-time.Minute).Unix(), AccountID: "acct_old",
	})

	jwt := makeJWT(t, "acct_new")
	var refreshes atomic.Int32
	src := codex.NewTokenSource(svc, codex.WithTokenRefresh(func(_ context.Context, refresh string) (openaicodex.Token, error) {
		refreshes.Add(1)
		if refresh != "refresh-old" {
			t.Errorf("refresh token = %q, want refresh-old", refresh)
		}
		return openaicodex.Token{
			Access: jwt, Refresh: "refresh-new",
			Expires: time.Now().Add(time.Hour), AccountID: "acct_new",
		}, nil
	}))

	tok, err := src.Token(t.Context())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if tok.Refresh != "refresh-new" || tok.AccountID != "acct_new" {
		t.Errorf("Token() = %+v, want refreshed token", tok)
	}
	if _, err := src.Token(t.Context()); err != nil {
		t.Fatalf("second Token() error = %v", err)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh count = %d, want 1", got)
	}

	stored, err := svc.GetKey(t.Context(), prefs.ScopeGlobal, codex.CredProvider)
	if err != nil {
		t.Fatalf("GetKey() error = %v", err)
	}
	var persisted codex.Cred
	if err := json.Unmarshal([]byte(stored), &persisted); err != nil {
		t.Fatalf("decode persisted credential: %v", err)
	}
	if persisted.Refresh != "refresh-new" {
		t.Errorf("persisted refresh = %q, want refresh-new", persisted.Refresh)
	}
}

func TestTokenSourceRefreshesNearExpiry(t *testing.T) {
	svc := openTestService(t)
	storeCred(t, svc, codex.Cred{
		Access: "old", Refresh: "refresh-near",
		ExpiresUnix: time.Now().Add(10 * time.Second).Unix(), AccountID: "acct_near",
	})
	var refreshes atomic.Int32
	src := codex.NewTokenSource(svc, codex.WithTokenRefresh(func(context.Context, string) (openaicodex.Token, error) {
		refreshes.Add(1)
		return openaicodex.Token{
			Access: makeJWT(t, "acct_new"), Refresh: "refresh-new",
			Expires: time.Now().Add(time.Hour), AccountID: "acct_new",
		}, nil
	}))
	if _, err := src.Token(t.Context()); err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refresh count = %d, want 1", got)
	}
}

func TestTokenSourceWithoutCredentialReturnsLoginHint(t *testing.T) {
	_, err := codex.NewTokenSource(openTestService(t)).Token(t.Context())
	if err == nil || !strings.Contains(err.Error(), "keys oauth") {
		t.Errorf("Token() error = %v, want keys oauth hint", err)
	}
}

func TestTokenSourcePropagatesRefreshFailure(t *testing.T) {
	svc := openTestService(t)
	storeCred(t, svc, codex.Cred{
		Access: "old", Refresh: "bad-refresh",
		ExpiresUnix: time.Now().Add(-time.Minute).Unix(), AccountID: "acct_x",
	})
	src := codex.NewTokenSource(svc, codex.WithTokenRefresh(func(context.Context, string) (openaicodex.Token, error) {
		return openaicodex.Token{}, errors.New("invalid grant")
	}))
	_, err := src.Token(t.Context())
	if err == nil || !strings.Contains(err.Error(), "refresh") {
		t.Errorf("Token() error = %v, want refresh error", err)
	}
}

func openTestService(t *testing.T) *prefs.Service {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	v, err := vault.Open(dir, func(_, _ bool) (string, error) { return "test-passphrase", nil })
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	svc := prefs.NewService(store, v, "")
	if _, err := svc.EnableCredentialProtection(t.Context()); err != nil {
		t.Fatal(err)
	}
	return svc
}

func storeCred(t *testing.T, svc *prefs.Service, cred codex.Cred) {
	t.Helper()
	raw, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if err := svc.SetKey(t.Context(), prefs.ScopeGlobal, codex.CredProvider, string(raw)); err != nil {
		t.Fatalf("SetKey() error = %v", err)
	}
}

func makeJWT(t *testing.T, accountID string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID},
	})
	if err != nil {
		t.Fatalf("marshal JWT payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(body) + ".sig"
}
