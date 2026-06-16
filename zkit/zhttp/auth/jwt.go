package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// UserID identifies the authenticated user a token was issued for — an
// alias of uuid.UUID so call sites read as auth.UserID without a wrapper
// type to convert at every boundary.
type UserID = uuid.UUID

// TokenType distinguishes an access token from a refresh token so
// the validator can reject cross-type confusion (e.g. presenting a
// refresh token at an access-token-protected endpoint). Both token
// types are JWTs signed with the same secret; without this claim
// they'd be interchangeable, and the refresh-token endpoint
// previously didn't verify the value at all.
type TokenType string

// The two token types carried in the Claims.Type claim.
const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// Claims represents JWT claims for both access and refresh tokens.
// Type discriminates between the two; verifiers MUST check it.
type Claims struct {
	UserID   UserID    `json:"user_id"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Type     TokenType `json:"type,omitempty"`
	jwt.RegisteredClaims
}

// TokenPair represents access and refresh tokens.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// JWTManager handles JWT operations.
type JWTManager struct {
	secretKey       []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	// issuer / audience are optional. When set they're stamped onto
	// every minted token AND enforced on validation, so a token signed
	// for one service can't be replayed at another that happens to
	// share the HMAC secret. Empty means "don't bind / don't check"
	// (backward compatible with managers constructed without options).
	issuer   string
	audience string
}

// JWTOption tunes a JWTManager at construction.
type JWTOption func(*JWTManager)

// WithIssuer binds minted tokens to iss and rejects tokens whose iss
// claim doesn't match on validation. Empty issuer (the default) skips
// the binding entirely.
func WithIssuer(iss string) JWTOption {
	return func(m *JWTManager) { m.issuer = iss }
}

// WithAudience binds minted tokens to aud and rejects tokens whose aud
// claim doesn't include it on validation. This is the cross-service
// replay guard: a token minted for "api.example.com" won't validate at
// a sibling service configured with a different audience even when the
// HMAC secret is shared. Empty audience (the default) skips the check.
func WithAudience(aud string) JWTOption {
	return func(m *JWTManager) { m.audience = aud }
}

// NewJWTManager creates a new JWT manager. Pass WithIssuer / WithAudience
// to bind tokens to a service identity and harden against cross-service
// replay.
func NewJWTManager(secretKey string, accessTTL, refreshTTL time.Duration, opts ...JWTOption) *JWTManager {
	m := &JWTManager{
		secretKey:       []byte(secretKey),
		accessTokenTTL:  accessTTL,
		refreshTokenTTL: refreshTTL,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// GenerateTokenPair generates a signed access JWT and a signed
// refresh JWT bound to (id, username, email). Both carry a Type
// claim so a refresh token can't be presented as an access token
// (or vice versa). The refresh token's user identity is the
// canonical source for RefreshAccessToken — earlier the handler
// trusted the access-token-derived context, which let any holder
// of a valid access token forge refresh flow without a real
// refresh credential.
func (j *JWTManager) GenerateTokenPair(id UserID, username, email string) (TokenPair, error) {
	now := time.Now()

	accessString, err := j.signClaims(now, id, username, email, TokenTypeAccess, j.accessTokenTTL)
	if err != nil {
		return TokenPair{}, fmt.Errorf("sign access token: %w", err)
	}

	refreshString, err := j.signClaims(now, id, username, email, TokenTypeRefresh, j.refreshTokenTTL)
	if err != nil {
		return TokenPair{}, fmt.Errorf("sign refresh token: %w", err)
	}

	return TokenPair{
		AccessToken:  accessString,
		RefreshToken: refreshString,
		ExpiresIn:    int64(j.accessTokenTTL.Seconds()),
	}, nil
}

// signClaims builds a Claims struct and HS256-signs it. Shared by
// access + refresh paths so the two token types differ only in the
// Type claim and TTL — no chance of a structural-mismatch
// regression when one path changes.
func (j *JWTManager) signClaims(
	now time.Time,
	id UserID,
	username, email string,
	t TokenType,
	ttl time.Duration,
) (string, error) {
	registered := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Subject:   id.String(),
	}
	if j.issuer != "" {
		registered.Issuer = j.issuer
	}
	if j.audience != "" {
		registered.Audience = jwt.ClaimStrings{j.audience}
	}
	claims := Claims{
		UserID:           id,
		Username:         username,
		Email:            email,
		Type:             t,
		RegisteredClaims: registered,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secretKey)
}

// ValidateToken validates and parses a JWT regardless of token type.
// Signature (HS256 only), expiry, not-before, and — when the manager
// was configured with them — issuer and audience are all enforced by
// the parser. It does NOT check the Type claim: refresh-token flows
// (MintAccessFromRefresh) need to validate a refresh token, so the
// type discrimination lives in the callers. Access-protected routes
// should use [JWTManager.ValidateAccessToken] instead.
func (j *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	keyFunc := func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.secretKey, nil
	}

	// WithValidMethods rejects alg confusion (e.g. "none", RS256) before
	// the key func even runs; WithExpirationRequired refuses tokens with
	// no exp. Issuer/audience are added only when configured.
	parserOpts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	}
	if j.issuer != "" {
		parserOpts = append(parserOpts, jwt.WithIssuer(j.issuer))
	}
	if j.audience != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(j.audience))
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, keyFunc, parserOpts...)
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	return claims, nil
}

// ValidateAccessToken validates a token AND requires its Type claim to
// be "access". Use this on access-protected routes so a refresh token
// (or a legacy token minted before the Type claim existed) can't be
// presented as an access credential. Everything ValidateToken enforces
// applies here too.
func (j *JWTManager) ValidateAccessToken(tokenString string) (*Claims, error) {
	claims, err := j.ValidateToken(tokenString)
	if err != nil {
		return nil, err
	}
	if claims.Type != TokenTypeAccess {
		return nil, fmt.Errorf("expected access token, got type %q", claims.Type)
	}
	return claims, nil
}

// MintAccessFromRefresh validates a refresh-token string and mints
// a fresh access token bound to the same identity. Replaces the
// older RefreshAccessToken which took (userID, username, email)
// directly — those came from the access-token-derived context, so
// the refresh-token VALUE wasn't checked at all. A dummy refresh
// cookie plus any valid access token was enough to refresh.
//
// Validation steps:
//   - Parse the JWT and verify the HS256 signature.
//   - Confirm the Type claim is "refresh"; reject access tokens
//     and unset Type (token issued by an older version).
//   - Standard time-based checks (exp, nbf) come from
//     jwt.ParseWithClaims's defaults.
//
// Note on revocation: the refresh token is still stateless. A
// stolen refresh token remains valid until expiry; rotation /
// server-side revocation would need a token store. The Type +
// signature checks here close the "any cookie passes" hole the
// audit flagged, but the revocation gap is a separate fix that
// requires backing storage.
func (j *JWTManager) MintAccessFromRefresh(refreshToken string) (string, error) {
	claims, err := j.ValidateToken(refreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh token: %w", err)
	}
	if claims.Type != TokenTypeRefresh {
		// Includes the "no Type claim" case for tokens issued by
		// the older path — those can't be trusted as refresh
		// credentials because the original code minted random
		// strings (not JWTs) AND access tokens with no Type.
		return "", fmt.Errorf("refresh token: wrong token type %q", claims.Type)
	}
	now := time.Now()
	return j.signClaims(now, claims.UserID, claims.Username, claims.Email, TokenTypeAccess, j.accessTokenTTL)
}
