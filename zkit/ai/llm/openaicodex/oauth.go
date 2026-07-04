// Package openaicodex implements an LLM provider that talks to
// OpenAI's ChatGPT Codex backend using a ChatGPT Plus/Pro OAuth
// credential instead of a paid OpenAI Platform API key.
//
// The protocol is the Responses API (not Chat Completions) hosted at
// chatgpt.com/backend-api/codex/responses. OAuth uses the same flow
// OpenAI's official Codex CLI uses (client_id, scopes, PKCE), so the
// constants in this file MUST stay byte-identical to upstream — the
// auth server rejects anything else.
package openaicodex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// oauthClient is the [zhttp.Client] every token-form POST in this
// package goes through. zhttp's defaults are right for OAuth token
// endpoints: 30s whole-request timeout (token POSTs are quick),
// transport-level dial / TLS / response-header timeouts, and retry
// with Retry-After honouring on 408 / 429 / 5xx — auth.openai.com
// emits 429 + Retry-After under throttling, which the previous
// http.DefaultClient call sites silently dropped.
//
// Tests swap this via [SetOAuthClientForTesting] to redirect
// auth.openai.com onto an httptest server. Don't reassign outside of
// tests — production code should treat it as effectively const.
var oauthClient = zhttp.NewClient()

// SetOAuthClientForTesting replaces the package-level OAuth client and
// returns a restore func the caller must defer. Test-only — tests
// build a [zhttp.Client] with [zhttp.WithTransport] pointed at their
// httptest server so the OAuth token endpoint round-trips through the
// fake. Production code should never call this.
func SetOAuthClientForTesting(c *zhttp.Client) func() {
	prev := oauthClient
	oauthClient = c
	return func() { oauthClient = prev }
}

// OAuth constants. These mirror the values the official Codex CLI
// sends. Changing any of them will break the auth handshake —
// auth.openai.com checks the client_id and originator against an
// allow-list, and PKCE/state must round-trip the exact bytes sent.
const (
	ClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	AuthorizeURL = "https://auth.openai.com/oauth/authorize"
	TokenURL     = "https://auth.openai.com/oauth/token"
	RedirectURI  = "http://localhost:1455/auth/callback"
	Scope        = "openid profile email offline_access"

	// jwtAccountClaimPath is the JWT claim key that wraps the chatgpt
	// account id. The access token's payload looks like:
	//   { "https://api.openai.com/auth": { "chatgpt_account_id": "..." } }
	jwtAccountClaimPath = "https://api.openai.com/auth"

	// originator marks the request as coming from the rust Codex CLI.
	// auth.openai.com requires this for the simplified-flow path.
	originatorCodex = "codex_cli_rs"
)

// Token is the credential bundle returned by a successful OAuth
// exchange or refresh. AccountID is the chatgpt-account-id header value
// extracted from the access token; the Codex backend requires it on
// every request.
type Token struct {
	Access    string
	Refresh   string
	Expires   time.Time
	AccountID string
}

// IsExpired reports whether the access token is at or past its expiry.
// The TokenSource implementation usually pads this with a refresh
// window (e.g. refresh when <60s remain) — this is the hard cutoff.
func (t Token) IsExpired() bool {
	return !t.Expires.IsZero() && !time.Now().Before(t.Expires)
}

// PKCE carries the verifier+challenge pair generated for a single
// authorisation flow. The verifier MUST be re-presented at the token
// exchange step; losing it means the code can't be redeemed.
type PKCE struct {
	Verifier  string
	Challenge string
}

// AuthorizationFlow is the bundle a caller needs to drive a user
// through the OAuth dance. URL is what to open in a browser; State and
// PKCE.Verifier must be kept around until the callback fires so the
// code can be exchanged.
type AuthorizationFlow struct {
	URL   string
	State string
	PKCE  PKCE
}

// ErrTokenExchange wraps non-2xx responses from auth.openai.com
// (both code-exchange and refresh). Callers can match against this to
// distinguish "user needs to re-auth" from transport errors.
var ErrTokenExchange = errors.New("openai oauth token exchange")

// ErrNoAccountID is returned when the JWT access token has no
// chatgpt_account_id claim. This usually means the OpenAI account
// doesn't have a ChatGPT subscription attached, so the Codex backend
// won't accept its tokens.
var ErrNoAccountID = errors.New("access token has no chatgpt_account_id claim")

// generatePKCE produces a fresh code_verifier + S256 challenge. The
// verifier is 32 random bytes base64url-encoded (no padding) per
// RFC 7636; the challenge is the SHA-256 of the verifier, also
// base64url-encoded without padding.
func generatePKCE() (PKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, fmt.Errorf("pkce verifier random: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return PKCE{Verifier: verifier, Challenge: challenge}, nil
}

// generateState returns a 16-byte hex string used as the OAuth `state`
// param. The local callback server compares this against the state in
// the redirect URL to defend against CSRF.
func generateState() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("state random: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// CreateAuthorizationFlow assembles the URL the user opens in a
// browser plus the verifier+state the caller needs to keep around for
// the callback step. Matches the exact param set the Codex CLI sends.
func CreateAuthorizationFlow() (AuthorizationFlow, error) {
	pkce, err := generatePKCE()
	if err != nil {
		return AuthorizationFlow{}, err
	}
	state, err := generateState()
	if err != nil {
		return AuthorizationFlow{}, err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", ClientID)
	q.Set("redirect_uri", RedirectURI)
	q.Set("scope", Scope)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", originatorCodex)
	return AuthorizationFlow{
		URL:   AuthorizeURL + "?" + q.Encode(),
		State: state,
		PKCE:  pkce,
	}, nil
}

// ParseAuthorizationInput pulls the `code` (and `state`, if present)
// out of whatever the user pasted: the full redirect URL, a
// `code=...&state=...` query fragment, a `code#state` shortcut, or
// the bare code on its own.
func ParseAuthorizationInput(input string) (string, string) {
	v := strings.TrimSpace(input)
	if v == "" {
		return "", ""
	}
	if u, err := url.Parse(v); err == nil && u.Scheme != "" {
		return u.Query().Get("code"), u.Query().Get("state")
	}
	if before, after, ok := strings.Cut(v, "#"); ok {
		return before, after
	}
	if strings.Contains(v, "code=") {
		if q, err := url.ParseQuery(v); err == nil {
			return q.Get("code"), q.Get("state")
		}
	}
	return v, ""
}

// tokenResponse is the shape auth.openai.com returns on both
// authorization_code and refresh_token grants.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// ExchangeAuthorizationCode swaps an authorization code (plus its PKCE
// verifier) for an access+refresh token pair. The returned Token's
// AccountID is extracted from the access token's JWT body — that
// extraction is the most likely point of failure for ChatGPT-free
// OpenAI accounts.
func ExchangeAuthorizationCode(ctx context.Context, code, verifier string) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", ClientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", RedirectURI)
	return postTokenForm(ctx, form)
}

// RefreshAccessToken trades a refresh token for a new access+refresh
// pair. The TokenSource layer calls this opportunistically when the
// current access token is near its expiry.
func RefreshAccessToken(ctx context.Context, refresh string) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refresh)
	form.Set("client_id", ClientID)
	return postTokenForm(ctx, form)
}

// postTokenForm is the shared POST-and-decode body for both the
// authorization_code and refresh_token grants. Both endpoints accept
// the same content-type and return the same response shape.
func postTokenForm(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := oauthClient.Do(ctx, req)
	if err != nil {
		return Token{}, fmt.Errorf("post token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return Token{}, fmt.Errorf("%w: status %d: %s", ErrTokenExchange, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Token{}, fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" || tr.ExpiresIn == 0 {
		return Token{}, fmt.Errorf("%w: token response missing fields", ErrTokenExchange)
	}
	accountID, err := DecodeAccountID(tr.AccessToken)
	if err != nil {
		return Token{}, err
	}
	return Token{
		Access:    tr.AccessToken,
		Refresh:   tr.RefreshToken,
		Expires:   time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		AccountID: accountID,
	}, nil
}

// DecodeAccountID extracts chatgpt_account_id from the JWT access
// token's middle segment. The token format is three base64url-encoded
// segments separated by dots; we only need the payload (segment 1).
// Returns ErrNoAccountID when the claim is absent — that's the signal
// "this OpenAI account isn't subscribed to ChatGPT".
func DecodeAccountID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("access token: expected 3 jwt segments, got %d", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some JWTs are padded; fall back to StdEncoding.
		if alt, altErr := base64.StdEncoding.DecodeString(parts[1]); altErr == nil {
			raw = alt
		} else {
			return "", fmt.Errorf("decode jwt payload: %w", err)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("parse jwt payload: %w", err)
	}
	auth, ok := payload[jwtAccountClaimPath].(map[string]any)
	if !ok {
		return "", ErrNoAccountID
	}
	id, ok := auth["chatgpt_account_id"].(string)
	if !ok || id == "" {
		return "", ErrNoAccountID
	}
	return id, nil
}

// TokenSource is the credential interface the codex Provider depends
// on. Implementations are expected to handle refresh + persistence
// transparently — the provider calls Token on every request and
// trusts whatever comes back.
//
// The interface lives here (consumer-side) because the provider is the
// only direct consumer. Other clients of openai_codex should not
// implement it; instead they should use the in-process implementation
// supplied by the zarlcode vault layer.
type TokenSource interface {
	Token(ctx context.Context) (Token, error)
}

// StaticTokenSource is a TokenSource backed by a single in-memory
// Token. It never refreshes — useful for tests and for one-off CLI
// usage where a refresh isn't worth wiring up. Real shells should use
// the vault-backed implementation in zarlcode/oauth.
type StaticTokenSource struct {
	T Token
}

// Token implements TokenSource.
func (s StaticTokenSource) Token(_ context.Context) (Token, error) {
	return s.T, nil
}
