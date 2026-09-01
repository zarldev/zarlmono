package openaicodex_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"testing"
	"time"

	openaicodex "github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
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

func TestStaticTokenSource(t *testing.T) {
	t.Parallel()
	ts := openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "a", Refresh: "r", AccountID: "acct"}}
	got, err := ts.Token(t.Context())
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
