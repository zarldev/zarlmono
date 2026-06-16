package auth_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/zhttp/auth"
)

const testSecret = "test-secret-key-32-bytes-or-more"

// ValidateAccessToken must reject a refresh token presented at an
// access-protected route — the hole the Auth middleware previously had
// by calling the type-agnostic ValidateToken.
func TestValidateAccessToken_RejectsRefreshToken(t *testing.T) {
	t.Parallel()
	mgr := auth.NewJWTManager(testSecret, time.Hour, 24*time.Hour)
	pair, err := mgr.GenerateTokenPair(uuid.New(), "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	if _, err := mgr.ValidateAccessToken(pair.RefreshToken); err == nil {
		t.Error("ValidateAccessToken accepted a refresh token; want type-mismatch error")
	}
	if _, err := mgr.ValidateAccessToken(pair.AccessToken); err != nil {
		t.Errorf("ValidateAccessToken rejected a valid access token: %v", err)
	}
}

// A token minted for one audience must not validate at a sibling service
// configured with a different audience, even though both share the HMAC
// secret. This is the cross-service replay guard.
func TestValidateToken_AudienceBlocksCrossServiceReplay(t *testing.T) {
	t.Parallel()
	svcA := auth.NewJWTManager(testSecret, time.Hour, 24*time.Hour, auth.WithAudience("api-a"))
	svcB := auth.NewJWTManager(testSecret, time.Hour, 24*time.Hour, auth.WithAudience("api-b"))

	pair, err := svcA.GenerateTokenPair(uuid.New(), "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	if _, err := svcA.ValidateToken(pair.AccessToken); err != nil {
		t.Fatalf("svcA rejected its own token: %v", err)
	}
	if _, err := svcB.ValidateToken(pair.AccessToken); err == nil {
		t.Error("svcB validated a token minted for api-a; audience replay guard failed")
	}
}

// Issuer binding is enforced the same way: a configured issuer must
// match on validation.
func TestValidateToken_IssuerMismatchRejected(t *testing.T) {
	t.Parallel()
	minter := auth.NewJWTManager(testSecret, time.Hour, 24*time.Hour, auth.WithIssuer("issuer-one"))
	checker := auth.NewJWTManager(testSecret, time.Hour, 24*time.Hour, auth.WithIssuer("issuer-two"))

	pair, err := minter.GenerateTokenPair(uuid.New(), "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	if _, err := minter.ValidateToken(pair.AccessToken); err != nil {
		t.Fatalf("minter rejected its own token: %v", err)
	}
	if _, err := checker.ValidateToken(pair.AccessToken); err == nil {
		t.Error("checker validated a token with the wrong issuer")
	}
}

// A manager configured with an audience must reject a token that carries
// no audience claim at all (e.g. minted by an un-bound manager) — the
// check can't be silently skipped by omission.
func TestValidateToken_AudienceRequiredWhenConfigured(t *testing.T) {
	t.Parallel()
	plain := auth.NewJWTManager(testSecret, time.Hour, 24*time.Hour)
	bound := auth.NewJWTManager(testSecret, time.Hour, 24*time.Hour, auth.WithAudience("api-a"))

	pair, err := plain.GenerateTokenPair(uuid.New(), "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	if _, err := bound.ValidateToken(pair.AccessToken); err == nil {
		t.Error("audience-bound manager accepted a token with no aud claim")
	}
}
