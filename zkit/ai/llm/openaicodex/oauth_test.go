package openaicodex_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	openaicodex "github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// makeJWT builds a fake three-segment JWT with the given payload. The
// signature is opaque garbage — DecodeAccountID never validates the
// signature, only the payload — so any non-empty third segment works.
func makeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	mid := base64.RawURLEncoding.EncodeToString(body)
	return head + "." + mid + ".sig"
}

func TestDecodeAccountID(t *testing.T) {
	t.Parallel()
	t.Run("present", func(t *testing.T) {
		t.Parallel()
		tok := makeJWT(t, map[string]any{
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct_abc123",
			},
		})
		got, err := openaicodex.DecodeAccountID(tok)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "acct_abc123" {
			t.Errorf("account id = %q, want %q", got, "acct_abc123")
		}
	})
	t.Run("claim missing", func(t *testing.T) {
		t.Parallel()
		tok := makeJWT(t, map[string]any{"sub": "user_xyz"})
		_, err := openaicodex.DecodeAccountID(tok)
		if !errors.Is(err, openaicodex.ErrNoAccountID) {
			t.Errorf("err = %v, want ErrNoAccountID", err)
		}
	})
	t.Run("not a jwt", func(t *testing.T) {
		t.Parallel()
		_, err := openaicodex.DecodeAccountID("not.a.jwt.plus.extra")
		if err == nil {
			t.Errorf("expected error for malformed jwt")
		}
	})
}

func TestParseAuthorizationInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		wantCode  string
		wantState string
	}{
		{"empty", "", "", ""},
		{"full callback url", "http://localhost:1455/auth/callback?code=abc&state=def", "abc", "def"},
		{"hash form", "abc#def", "abc", "def"},
		{"query fragment", "code=abc&state=def", "abc", "def"},
		{"bare code", "abc123", "abc123", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, state := openaicodex.ParseAuthorizationInput(tt.input)
			if code != tt.wantCode || state != tt.wantState {
				t.Errorf("got (%q, %q), want (%q, %q)", code, state, tt.wantCode, tt.wantState)
			}
		})
	}
}

func TestCreateAuthorizationFlow(t *testing.T) {
	t.Parallel()
	flow, err := openaicodex.CreateAuthorizationFlow()
	if err != nil {
		t.Fatalf("CreateAuthorizationFlow: %v", err)
	}
	if flow.PKCE.Verifier == "" || flow.PKCE.Challenge == "" {
		t.Errorf("pkce verifier/challenge empty: %+v", flow.PKCE)
	}
	if flow.PKCE.Verifier == flow.PKCE.Challenge {
		t.Errorf("verifier and challenge identical — S256 not applied")
	}
	if flow.State == "" {
		t.Errorf("state empty")
	}
	u, err := url.Parse(flow.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != openaicodex.ClientID {
		t.Errorf("client_id = %q, want %q", q.Get("client_id"), openaicodex.ClientID)
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("challenge method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") != flow.PKCE.Challenge {
		t.Errorf("url challenge mismatch")
	}
	if q.Get("state") != flow.State {
		t.Errorf("url state mismatch")
	}
	if q.Get("originator") != "codex_cli_rs" {
		t.Errorf("originator = %q, want codex_cli_rs", q.Get("originator"))
	}
	if q.Get("redirect_uri") != openaicodex.RedirectURI {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), openaicodex.RedirectURI)
	}
}

// fakeTokenServer is an httptest backend that mimics auth.openai.com's
// token endpoint. It records the last form posted (for assertions) and
// returns scripted responses.
type fakeTokenServer struct {
	t        *testing.T
	respond  func(form url.Values) (status int, body string)
	lastForm url.Values
}

func (f *fakeTokenServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		f.lastForm = form
		status, resp := f.respond(form)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}
}

// redirectTokenURL stands up the httptest server's transport on the
// package's OAuth client for the duration of the test, then restores
// the previous client. Retries are disabled — refresh tests assert
// exact request counts, and a retry on a transient httptest failure
// would muddy the counter (the retry path has its own scenarios).
//
// Tests that touch the OAuth client share global state and so must
// not run in parallel.
func redirectTokenURL(t *testing.T, srv *httptest.Server) {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	restore := openaicodex.SetOAuthClientForTesting(zhttp.NewClient(
		zhttp.WithTransport(&rewriteTransport{target: target}),
		zhttp.WithRetryPolicy(zhttp.NoRetry()),
	))
	t.Cleanup(restore)
}

// rewriteTransport sends every outbound request to the test server's
// host while preserving the original path. This lets us point the
// pinned auth.openai.com TokenURL at httptest without exposing a
// "with base url" knob on the package surface.
type rewriteTransport struct {
	target *url.URL
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// Not parallel — mutates the package-level OAuth client via
// [openaicodex.SetOAuthClientForTesting]. The other tests sharing
// that seam are likewise sequential.
func TestExchangeAuthorizationCode(t *testing.T) {
	jwt := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_xyz",
		},
	})
	fake := &fakeTokenServer{t: t, respond: func(form url.Values) (int, string) {
		if form.Get("grant_type") != "authorization_code" {
			return http.StatusBadRequest, `{"error":"wrong grant"}`
		}
		if form.Get("code") != "the-code" {
			return http.StatusBadRequest, `{"error":"wrong code"}`
		}
		if form.Get("code_verifier") != "the-verifier" {
			return http.StatusBadRequest, `{"error":"wrong verifier"}`
		}
		return http.StatusOK, `{"access_token":"` + jwt + `","refresh_token":"r1","expires_in":3600}`
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	redirectTokenURL(t, srv)

	tok, err := openaicodex.ExchangeAuthorizationCode(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatalf("ExchangeAuthorizationCode: %v", err)
	}
	if tok.Access != jwt {
		t.Errorf("access mismatch")
	}
	if tok.Refresh != "r1" {
		t.Errorf("refresh = %q, want r1", tok.Refresh)
	}
	if tok.AccountID != "acct_xyz" {
		t.Errorf("account id = %q, want acct_xyz", tok.AccountID)
	}
	if time.Until(tok.Expires) < time.Hour-time.Minute {
		t.Errorf("expires too soon: %v", tok.Expires)
	}
	if fake.lastForm.Get("client_id") != openaicodex.ClientID {
		t.Errorf("client_id missing in form")
	}
	if fake.lastForm.Get("redirect_uri") != openaicodex.RedirectURI {
		t.Errorf("redirect_uri missing in form")
	}
}

// Not parallel — shares the OAuth-client seam (see
// [TestExchangeAuthorizationCode]).
func TestExchangeAuthorizationCode_Failure(t *testing.T) {
	fake := &fakeTokenServer{t: t, respond: func(form url.Values) (int, string) {
		return http.StatusUnauthorized, `{"error":"invalid_grant"}`
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	redirectTokenURL(t, srv)
	_, err := openaicodex.ExchangeAuthorizationCode(context.Background(), "x", "y")
	if !errors.Is(err, openaicodex.ErrTokenExchange) {
		t.Errorf("err = %v, want ErrTokenExchange", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want status 401 in message", err)
	}
}

// Not parallel — shares the OAuth-client seam (see
// [TestExchangeAuthorizationCode]).
func TestRefreshAccessToken(t *testing.T) {
	jwt := makeJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_refresh",
		},
	})
	fake := &fakeTokenServer{t: t, respond: func(form url.Values) (int, string) {
		if form.Get("grant_type") != "refresh_token" {
			return http.StatusBadRequest, `{"error":"wrong grant"}`
		}
		if form.Get("refresh_token") != "old-refresh" {
			return http.StatusBadRequest, `{"error":"wrong refresh"}`
		}
		return http.StatusOK, `{"access_token":"` + jwt + `","refresh_token":"new-refresh","expires_in":1800}`
	}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	redirectTokenURL(t, srv)

	tok, err := openaicodex.RefreshAccessToken(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("RefreshAccessToken: %v", err)
	}
	if tok.Refresh != "new-refresh" {
		t.Errorf("refresh = %q, want new-refresh", tok.Refresh)
	}
	if tok.AccountID != "acct_refresh" {
		t.Errorf("account id = %q, want acct_refresh", tok.AccountID)
	}
}

func TestStaticTokenSource(t *testing.T) {
	t.Parallel()
	ts := openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "a", Refresh: "r", AccountID: "acct"}}
	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got.Access != "a" || got.AccountID != "acct" {
		t.Errorf("static token mismatch: %+v", got)
	}
}

func TestTokenIsExpired(t *testing.T) {
	t.Parallel()
	past := openaicodex.Token{Expires: time.Now().Add(-time.Minute)}
	future := openaicodex.Token{Expires: time.Now().Add(time.Minute)}
	zero := openaicodex.Token{}
	if !past.IsExpired() {
		t.Errorf("past token should be expired")
	}
	if future.IsExpired() {
		t.Errorf("future token should not be expired")
	}
	if zero.IsExpired() {
		t.Errorf("zero-value token should not be expired")
	}
}
