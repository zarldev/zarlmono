package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/prefs"
)

// CredProvider is the canonical provider key under which Codex
// OAuth credentials are stored in the api_keys table. Reusing the
// existing table (instead of carving out a new "oauth_credentials"
// schema) keeps a single AES-GCM ciphertext per (workspace, provider)
// and lets `zarlcode keys list` surface the presence of an OAuth
// cred alongside API keys for free.
const CredProvider = "openai-codex"

// refreshLeeway is how close to expiry we trigger a proactive
// refresh. Picked to be larger than typical request RTTs so an
// in-flight request doesn't get a 401 from the backend just because
// the token aged out between header serialisation and arrival.
const refreshLeeway = 60 * time.Second

// Cred is the persisted shape inside the vault's ciphertext.
// `expires_unix` carries the absolute deadline (not a duration) so the
// blob is self-contained — a process that reads it five hours later
// can decide for itself whether to refresh.
type Cred struct {
	Access      string `json:"access"`
	Refresh     string `json:"refresh"`
	ExpiresUnix int64  `json:"expires_unix"`
	AccountID   string `json:"account_id"`
}

// toToken converts the persisted shape into the openaicodex.Token the
// provider consumes.
func (c Cred) toToken() openaicodex.Token {
	return openaicodex.Token{
		Access:    c.Access,
		Refresh:   c.Refresh,
		Expires:   time.Unix(c.ExpiresUnix, 0),
		AccountID: c.AccountID,
	}
}

// credFromToken builds the persisted shape from a freshly
// exchanged Token.
func credFromToken(t openaicodex.Token) Cred {
	return Cred{
		Access:      t.Access,
		Refresh:     t.Refresh,
		ExpiresUnix: t.Expires.Unix(),
		AccountID:   t.AccountID,
	}
}

// tokenSource implements openaicodex.TokenSource against the
// zarlcode [prefs.Service] (which wraps both the api_keys vault
// and the db.Store). On every Token call it reads the current
// ciphertext, refreshes opportunistically when nearing expiry, and
// writes the new ciphertext back to the SAME scope it was loaded
// from so refresh-from-global stays global rather than silently
// creating a workspace pin.
//
// Refresh is serialised by mu — two goroutines hitting Token at the
// same instant collapse to one refresh attempt. Cross-process
// serialisation is best-effort: another process may refresh in
// parallel; the loser of that race re-reads the new ciphertext on its
// next Token call. The Codex auth server is idempotent enough that
// occasional double refreshes don't break anything (each refresh
// returns a fresh token bundle).
type tokenSource struct {
	svc *prefs.Service

	mu sync.Mutex
}

// NewTokenSource constructs the source. The service's wsRoot
// determines which workspace's row the read prefers; the GLOBAL row
// is the fallback if the workspace has no pin. Writes go back to the
// scope the read resolved from.
//
// The HTTP client used for token refresh lives inside the
// [openaicodex] package — tests redirect auth.openai.com via
// [openaicodex.SetOAuthClientForTesting].
func NewTokenSource(svc *prefs.Service) *tokenSource {
	return &tokenSource{svc: svc}
}

// Token implements openaicodex.TokenSource.
//
// The contract: returns a token whose Access is good for the next
// refreshLeeway window. If we can't satisfy that, it returns an
// error — the provider treats that as a fatal "user needs to re-auth"
// rather than retrying.
func (s *tokenSource) Token(ctx context.Context) (openaicodex.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cred, src, ok, err := s.readCred(ctx)
	if err != nil {
		return openaicodex.Token{}, err
	}
	if !ok {
		return openaicodex.Token{}, fmt.Errorf(
			"openaicodex: no stored credential — run `zarlcode keys oauth %s`",
			CredProvider,
		)
	}
	tok := cred.toToken()
	if !needsRefresh(tok) {
		return tok, nil
	}
	if tok.Refresh == "" {
		return openaicodex.Token{}, errors.New("openaicodex: stored credential has no refresh token — re-auth required")
	}
	refreshed, err := openaicodex.RefreshAccessToken(ctx, tok.Refresh)
	if err != nil {
		return openaicodex.Token{}, fmt.Errorf("openaicodex: refresh: %w", err)
	}
	if err := s.writeCred(ctx, src, credFromToken(refreshed)); err != nil {
		// Surface the write error rather than silently returning a
		// token we can't persist — a downstream caller may rely on
		// the new refresh token being durable.
		return openaicodex.Token{}, fmt.Errorf("openaicodex: persist refreshed credential: %w", err)
	}
	return refreshed, nil
}

// needsRefresh is true when the access token is at-or-past its expiry
// minus the leeway window. Zero-value expiries (e.g. tokens written
// before the persisted format existed) are treated as in-need so the
// vault gets healed on first use.
func needsRefresh(t openaicodex.Token) bool {
	if t.Expires.IsZero() {
		return true
	}
	return time.Now().Add(refreshLeeway).After(t.Expires)
}

// readCred fetches and decodes the persisted Codex credential, plus
// the scope it resolved from (so writeCred can persist back to the
// same row).
//
// Returns (cred, src, true, nil) on success, (zero, zero, false, nil)
// when neither workspace nor global has an entry, or (zero, zero,
// false, err) on any other failure.
func (s *tokenSource) readCred(ctx context.Context) (Cred, prefs.Scope, bool, error) {
	kv, ok, err := s.svc.GetKeyEffective(ctx, CredProvider)
	if err != nil || !ok {
		return Cred{}, 0, ok, err
	}
	var c Cred
	if err := json.Unmarshal([]byte(kv.Value), &c); err != nil {
		return Cred{}, 0, false, fmt.Errorf("decode codex credential: %w", err)
	}
	return c, kv.Source, true, nil
}

// writeCred persists a credential at the explicit scope the read
// resolved from. Refresh-from-global stays global; refresh-from-
// workspace stays workspace — the row the user originally pinned
// against doesn't drift.
func (s *tokenSource) writeCred(ctx context.Context, sc prefs.Scope, c Cred) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode codex credential: %w", err)
	}
	return s.svc.SetKey(ctx, sc, CredProvider, string(raw))
}
