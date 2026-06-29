package codex

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/db"
	"github.com/zarldev/zarlmono/zkit/prefs"
	"github.com/zarldev/zarlmono/zkit/vault"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// redirectOAuthClient wires openaicodex's package-level OAuth client
// through a transport that rewrites auth.openai.com onto the test's
// httptest server. Retries are disabled — refresh tests assert exact
// request counts, and retry on a transient httptest failure would
// muddy the counter. Restored via t.Cleanup.
func redirectOAuthClient(t *testing.T, target *url.URL) {
	t.Helper()
	restore := openaicodex.SetOAuthClientForTesting(zhttp.NewClient(
		zhttp.WithTransport(&rewriteRoundTripper{target: target}),
		zhttp.WithRetryPolicy(zhttp.NoRetry()),
	))
	t.Cleanup(restore)
}

// openTestStoreAndVault opens a fresh SQLite store + vault rooted under
// t.TempDir(). The vault uses a deterministic master key passed
// through $ZARLCODE_KEY so two helpers in the same test see the
// same ciphertext bytes. Distinct from openTestStore in session_test.go,
// which returns only the store.
func openTestStoreAndVault(t *testing.T) (*db.Store, *vault.Vault) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), ".zarlcode")
	t.Setenv("ZARLCODE_HOME", dir)
	// 32-byte key, base64-encoded.
	t.Setenv("ZARLCODE_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))

	store, err := db.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	v, err := vault.Open(nil)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return store, v
}

// makeJWTPayload builds a JWT carrying the chatgpt_account_id claim.
// Same shape as the helper in pkg/ai/llm/openaicodex/oauth_test.go;
// duplicated here so this package's tests stay self-contained.
func makeJWTPayload(t *testing.T, accountID string) string {
	t.Helper()
	payload := map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	mid := base64.RawURLEncoding.EncodeToString(body)
	return head + "." + mid + ".sig"
}

// rewriteRoundTripper sends every outbound request to the test
// server's host while preserving the request path. Used to redirect
// the package's pinned TokenURL constant onto httptest without
// exposing a "with base url" knob.
type rewriteRoundTripper struct {
	target *url.URL
}

func (rt *rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestCodexTokenSource_ReturnsCachedTokenWhenFresh(t *testing.T) {
	store, v := openTestStoreAndVault(t)
	jwt := makeJWTPayload(t, "acct_fresh")
	cred := Cred{
		Access:      jwt,
		Refresh:     "refresh-1",
		ExpiresUnix: time.Now().Add(time.Hour).Unix(),
		AccountID:   "acct_fresh",
	}
	raw, _ := json.Marshal(cred)
	if err := prefs.NewService(store, v, "").SetKey(t.Context(), prefs.ScopeGlobal, CredProvider, string(raw)); err != nil {
		t.Fatalf("setStoredAPIKey: %v", err)
	}
	src := NewTokenSource(prefs.NewService(store, v, ""))

	// No refresh server — if the source tries to hit the network we'll
	// see an EOF / dial error.
	redirectOAuthClient(t, mustURL(t, "http://127.0.0.1:1"))

	tok, err := src.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.Access != jwt {
		t.Errorf("access mismatch")
	}
	if tok.AccountID != "acct_fresh" {
		t.Errorf("account id = %q", tok.AccountID)
	}
}

func TestCodexTokenSource_RefreshesWhenExpired(t *testing.T) {
	store, v := openTestStoreAndVault(t)

	// Initial credential with expiry already past.
	oldCred := Cred{
		Access:      "old-access",
		Refresh:     "refresh-old",
		ExpiresUnix: time.Now().Add(-time.Minute).Unix(),
		AccountID:   "acct_old",
	}
	raw, _ := json.Marshal(oldCred)
	if err := prefs.NewService(store, v, "").SetKey(t.Context(), prefs.ScopeGlobal, CredProvider, string(raw)); err != nil {
		t.Fatalf("setStoredAPIKey: %v", err)
	}

	// Refresh server returns a brand-new token bundle.
	jwt := makeJWTPayload(t, "acct_new")
	var refreshCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCount.Add(1)
		body, _ := io.ReadAll(r.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil || form.Get("grant_type") != "refresh_token" {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if form.Get("refresh_token") != "refresh-old" {
			http.Error(w, "wrong refresh", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"`+jwt+`","refresh_token":"refresh-new","expires_in":3600}`)
	}))
	defer srv.Close()

	src := NewTokenSource(prefs.NewService(store, v, ""))
	redirectOAuthClient(t, mustURL(t, srv.URL))

	tok, err := src.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.Refresh != "refresh-new" {
		t.Errorf("refresh = %q, want refresh-new", tok.Refresh)
	}
	if tok.AccountID != "acct_new" {
		t.Errorf("account id = %q, want acct_new", tok.AccountID)
	}

	// Persisted blob should now carry the new refresh token.
	stored, ok, err := prefs.NewService(store, v, "").GetKey(t.Context(), prefs.ScopeGlobal, CredProvider)
	if err != nil || !ok {
		t.Fatalf("getStoredAPIKey: ok=%v err=%v", ok, err)
	}
	var persisted Cred
	if err := json.Unmarshal([]byte(stored), &persisted); err != nil {
		t.Fatalf("decode persisted: %v", err)
	}
	if persisted.Refresh != "refresh-new" {
		t.Errorf("persisted refresh = %q, want refresh-new", persisted.Refresh)
	}

	// Second call should NOT hit the refresh server again — token now
	// has a future expiry.
	if _, err := src.Token(t.Context()); err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if got := refreshCount.Load(); got != 1 {
		t.Errorf("refresh count = %d, want 1 (second call should be cached)", got)
	}
}

func TestCodexTokenSource_RefreshesNearExpiry(t *testing.T) {
	store, v := openTestStoreAndVault(t)

	// Expiry is 10s away — inside the leeway window, so should refresh.
	near := Cred{
		Access:      "old-access",
		Refresh:     "refresh-near",
		ExpiresUnix: time.Now().Add(10 * time.Second).Unix(),
		AccountID:   "acct_near",
	}
	raw, _ := json.Marshal(near)
	if err := prefs.NewService(store, v, "").SetKey(t.Context(), prefs.ScopeGlobal, CredProvider, string(raw)); err != nil {
		t.Fatalf("setStoredAPIKey: %v", err)
	}

	jwt := makeJWTPayload(t, "acct_near_refreshed")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"`+jwt+`","refresh_token":"refresh-fresh","expires_in":3600}`)
	}))
	defer srv.Close()

	src := NewTokenSource(prefs.NewService(store, v, ""))
	redirectOAuthClient(t, mustURL(t, srv.URL))

	tok, err := src.Token(t.Context())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok.Refresh != "refresh-fresh" {
		t.Errorf("refresh = %q, want refresh-fresh", tok.Refresh)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
}

func TestCodexTokenSource_NoCredReturnsHelpfulError(t *testing.T) {
	store, v := openTestStoreAndVault(t)
	src := NewTokenSource(prefs.NewService(store, v, ""))
	_, err := src.Token(t.Context())
	if err == nil {
		t.Fatalf("expected error when vault is empty")
	}
	if !strings.Contains(err.Error(), "keys oauth") {
		t.Errorf("err = %v, want hint to run keys oauth", err)
	}
}

func TestCodexTokenSource_RefreshFailurePropagates(t *testing.T) {
	store, v := openTestStoreAndVault(t)
	expired := Cred{
		Access:      "old",
		Refresh:     "bad-refresh",
		ExpiresUnix: time.Now().Add(-time.Minute).Unix(),
		AccountID:   "acct_x",
	}
	raw, _ := json.Marshal(expired)
	if err := prefs.NewService(store, v, "").SetKey(t.Context(), prefs.ScopeGlobal, CredProvider, string(raw)); err != nil {
		t.Fatalf("setStoredAPIKey: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()
	src := NewTokenSource(prefs.NewService(store, v, ""))
	redirectOAuthClient(t, mustURL(t, srv.URL))
	_, err := src.Token(t.Context())
	if err == nil {
		t.Fatalf("expected error from failed refresh")
	}
	if !strings.Contains(err.Error(), "refresh") {
		t.Errorf("err = %v, want it to mention refresh", err)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u
}
