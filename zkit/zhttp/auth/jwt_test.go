package auth_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/zarldev/zarlmono/zkit/zhttp/auth"
)

// Regression: refresh tokens MUST be JWTs with type=refresh, and
// MintAccessFromRefresh MUST validate the value, not trust caller
// identity. Earlier the handler only checked that *some* cookie
// existed and minted from request-context identity — any valid
// access token + dummy cookie was a refresh.
func TestMintAccessFromRefresh_RejectsBogusCookie(t *testing.T) {
	t.Parallel()
	mgr := auth.NewJWTManager("test-secret-key-32-bytes-or-more", time.Hour, 24*time.Hour)
	if _, err := mgr.MintAccessFromRefresh("not-a-jwt"); err == nil {
		t.Error("MintAccessFromRefresh accepted a junk string; want error")
	}
	if _, err := mgr.MintAccessFromRefresh(""); err == nil {
		t.Error("MintAccessFromRefresh accepted an empty string; want error")
	}
}

func TestMintAccessFromRefresh_RejectsAccessTokenAsRefresh(t *testing.T) {
	t.Parallel()
	mgr := auth.NewJWTManager("test-secret-key-32-bytes-or-more", time.Hour, 24*time.Hour)
	pair, err := mgr.GenerateTokenPair(uuid.New(), "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	if _, err := mgr.MintAccessFromRefresh(pair.AccessToken); err == nil {
		t.Error("MintAccessFromRefresh accepted an access token; want type-mismatch error")
	}
}

func TestMintAccessFromRefresh_AcceptsValidRefreshToken(t *testing.T) {
	t.Parallel()
	mgr := auth.NewJWTManager("test-secret-key-32-bytes-or-more", time.Hour, 24*time.Hour)
	id := uuid.New()
	pair, err := mgr.GenerateTokenPair(id, "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	access, err := mgr.MintAccessFromRefresh(pair.RefreshToken)
	if err != nil {
		t.Fatalf("MintAccessFromRefresh with valid refresh: %v", err)
	}
	claims, err := mgr.ValidateToken(access)
	if err != nil {
		t.Fatalf("ValidateToken on minted access: %v", err)
	}
	if claims.UserID != id {
		t.Errorf("minted access user_id = %v, want %v (must come from refresh token, not context)", claims.UserID, id)
	}
	if claims.Type != auth.TokenTypeAccess {
		t.Errorf("minted token type = %q, want %q", claims.Type, auth.TokenTypeAccess)
	}
}

func TestMintAccessFromRefresh_RejectsExpiredRefresh(t *testing.T) {
	t.Parallel()
	// Refresh TTL of 1ns → token is born expired.
	mgr := auth.NewJWTManager("test-secret-key-32-bytes-or-more", time.Hour, time.Nanosecond)
	pair, err := mgr.GenerateTokenPair(uuid.New(), "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // ensure exp is in the past
	if _, err := mgr.MintAccessFromRefresh(pair.RefreshToken); err == nil {
		t.Error("MintAccessFromRefresh accepted an expired refresh token; want error")
	}
}

func TestGenerateTokenPair_AccessAndRefreshHaveDifferentTypes(t *testing.T) {
	t.Parallel()
	mgr := auth.NewJWTManager("test-secret-key-32-bytes-or-more", time.Hour, 24*time.Hour)
	pair, err := mgr.GenerateTokenPair(uuid.New(), "alice", "alice@example.com")
	if err != nil {
		t.Fatalf("GenerateTokenPair: %v", err)
	}
	if pair.AccessToken == pair.RefreshToken {
		t.Fatal("access and refresh tokens are identical")
	}
	accessClaims, err := mgr.ValidateToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("validate access: %v", err)
	}
	refreshClaims, err := mgr.ValidateToken(pair.RefreshToken)
	if err != nil {
		t.Fatalf("validate refresh: %v", err)
	}
	if accessClaims.Type != auth.TokenTypeAccess {
		t.Errorf("access token type = %q, want %q", accessClaims.Type, auth.TokenTypeAccess)
	}
	if refreshClaims.Type != auth.TokenTypeRefresh {
		t.Errorf("refresh token type = %q, want %q", refreshClaims.Type, auth.TokenTypeRefresh)
	}
	// JWT shape sanity: both should be three-dot-separated.
	if strings.Count(pair.RefreshToken, ".") != 2 {
		t.Errorf("refresh token isn't a JWT: %q", pair.RefreshToken)
	}
}
