package auth

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// Middleware bundles JWT validation and cookie management. CSRF is no
// longer the responsibility of this package — wire
// zkit/zhttp/middleware.CrossOrigin() at the router instead.
type Middleware struct {
	JWTManager    *JWTManager
	CookieManager *CookieManager
}

// NewMiddleware constructs the auth middleware bundle.
func NewMiddleware(jwtManager *JWTManager, isProduction bool) *Middleware {
	return &Middleware{
		JWTManager:    jwtManager,
		CookieManager: NewCookieManager(isProduction, ""),
	}
}

// Auth validates JWT tokens and adds the user to the request context.
// Token is read from cookie first, falling back to a Bearer header.
func (m *Middleware) Auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := extractToken(r)
		if !ok {
			zhttp.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		claims, err := m.JWTManager.ValidateAccessToken(token)
		if err != nil {
			slog.DebugContext(r.Context(), "auth: invalid token", "error", err)
			zhttp.WriteError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		ctx := SetUserID(r.Context(), claims.UserID)
		ctx = SetUser(ctx, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// OptionalAuth attaches the user to the context if a valid token is
// present, but never rejects the request.
func (m *Middleware) OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := extractToken(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		claims, err := m.JWTManager.ValidateAccessToken(token)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := SetUserID(r.Context(), claims.UserID)
		ctx = SetUser(ctx, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// extractToken pulls a JWT from the auth_token cookie or, failing
// that, an "Authorization: Bearer …" header.
func extractToken(r *http.Request) (string, bool) {
	if token, err := GetTokenFromCookie(r); err == nil && token != "" {
		return token, true
	}
	const bearer = "Bearer "
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, bearer) {
		return "", false
	}
	return strings.TrimPrefix(authHeader, bearer), true
}
