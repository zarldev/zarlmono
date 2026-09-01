package openaicodex_test

import (
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

	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// makeTokenJWT builds a fake three-segment JWT with the given payload.
func makeTokenJWT(t *testing.T, payload map[string]any) string {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	middle := base64.RawURLEncoding.EncodeToString(body)
	return head + "." + middle + ".sig"
}

type fakeTokenServer struct {
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
		status, response := f.respond(form)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}
}

func tokenTestClient(t *testing.T, srv *httptest.Server) *zhttp.Client {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return zhttp.NewClient(
		zhttp.WithTransport(&rewriteTransport{target: target}),
		zhttp.WithRetryPolicy(zhttp.NoRetry()),
	)
}

type rewriteTransport struct {
	target *url.URL
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestPostTokenFormAuthorizationCode(t *testing.T) {
	t.Parallel()
	jwt := makeTokenJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_xyz",
		},
	})
	fake := &fakeTokenServer{respond: func(form url.Values) (int, string) {
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
	t.Cleanup(srv.Close)

	tok, err := openaicodex.ExchangeAuthorizationCode(t.Context(), "the-code", "the-verifier", openaicodex.WithOAuthEndpoint(tokenTestClient(t, srv), openaicodex.TokenURL))
	if err != nil {
		t.Fatalf("postTokenForm: %v", err)
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

func TestPostTokenFormFailure(t *testing.T) {
	t.Parallel()
	fake := &fakeTokenServer{respond: func(url.Values) (int, string) {
		return http.StatusUnauthorized, `{"error":"invalid_grant"}`
	}}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	_, err := openaicodex.ExchangeAuthorizationCode(t.Context(), "code", "verifier", openaicodex.WithOAuthEndpoint(tokenTestClient(t, srv), openaicodex.TokenURL))
	if !errors.Is(err, openaicodex.ErrTokenExchange) {
		t.Errorf("err = %v, want openaicodex.ErrTokenExchange", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v, want status 401 in message", err)
	}
}

func TestPostTokenFormResponseTooLarge(t *testing.T) {
	t.Parallel()
	fake := &fakeTokenServer{respond: func(url.Values) (int, string) {
		return http.StatusOK, strings.Repeat("x", 1<<20+1)
	}}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	_, err := openaicodex.ExchangeAuthorizationCode(t.Context(), "code", "verifier", openaicodex.WithOAuthEndpoint(tokenTestClient(t, srv), openaicodex.TokenURL))
	if !errors.Is(err, openaicodex.ErrTokenExchange) {
		t.Errorf("err = %v, want openaicodex.ErrTokenExchange", err)
	}
	if !strings.Contains(err.Error(), "response exceeds 1 MiB") {
		t.Errorf("err = %v, want response size limit in message", err)
	}
}

func TestPostTokenFormRefresh(t *testing.T) {
	t.Parallel()
	jwt := makeTokenJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_refresh",
		},
	})
	fake := &fakeTokenServer{respond: func(form url.Values) (int, string) {
		if form.Get("grant_type") != "refresh_token" {
			return http.StatusBadRequest, `{"error":"wrong grant"}`
		}
		if form.Get("refresh_token") != "old-refresh" {
			return http.StatusBadRequest, `{"error":"wrong refresh"}`
		}
		return http.StatusOK, `{"access_token":"` + jwt + `","refresh_token":"new-refresh","expires_in":1800}`
	}}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	tok, err := openaicodex.RefreshAccessToken(t.Context(), "old-refresh", openaicodex.WithOAuthEndpoint(tokenTestClient(t, srv), openaicodex.TokenURL))
	if err != nil {
		t.Fatalf("postTokenForm: %v", err)
	}
	if tok.Refresh != "new-refresh" {
		t.Errorf("refresh = %q, want new-refresh", tok.Refresh)
	}
	if tok.AccountID != "acct_refresh" {
		t.Errorf("account id = %q, want acct_refresh", tok.AccountID)
	}
}
