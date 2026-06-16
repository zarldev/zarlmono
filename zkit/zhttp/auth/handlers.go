package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// Handlers provides reusable HTTP handlers for authentication.
type Handlers struct {
	jwtManager      *JWTManager
	cookieManager   *CookieManager
	authService     Service
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

// NewHandlers wires the handler bundle. CSRF lives at the router
// level now (pkg/zhttp/middleware.CrossOrigin) — no token cache or TTL
// here.
func NewHandlers(mw *Middleware, authService Service, accessTokenTTL, refreshTokenTTL time.Duration) *Handlers {
	return &Handlers{
		jwtManager:      mw.JWTManager,
		cookieManager:   mw.CookieManager,
		authService:     authService,
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
	}
}

// Register handles user registration.
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := zhttp.DecodeJSON(r, &req, 0); err != nil {
		zhttp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	response, err := h.authService.Register(r.Context(), req)
	if err != nil {
		h.handleAuthError(w, err, "registration")
		return
	}
	h.setAuthCookies(w, response)
	zhttp.WriteJSON(w, http.StatusCreated, response)
}

// Login handles user authentication.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := zhttp.DecodeJSON(r, &req, 0); err != nil {
		slog.ErrorContext(r.Context(), "decode login request", "error", err)
		zhttp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	response, err := h.authService.Login(r.Context(), req)
	if err != nil {
		h.handleAuthError(w, err, "login")
		return
	}
	h.setAuthCookies(w, response)
	zhttp.WriteJSON(w, http.StatusOK, response)
}

// Logout clears auth cookies.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	h.cookieManager.ClearAuthCookies(w)
	zhttp.WriteJSON(w, http.StatusOK, MessageResponse{Message: "logged out successfully"})
}

// RefreshToken mints a new access token from a valid refresh JWT
// presented in the refresh_token cookie. Identity comes from the
// refresh token's own claims, NOT from request context — earlier
// the handler trusted UserIDFromContext (populated by access-token
// middleware), which made a valid access token + any refresh
// cookie sufficient to mint a new access token. The refresh
// credential value was effectively ignored.
func (h *Handlers) RefreshToken(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil {
		zhttp.WriteError(w, http.StatusUnauthorized, "refresh token required")
		return
	}
	newAccessToken, err := h.jwtManager.MintAccessFromRefresh(cookie.Value)
	if err != nil {
		zhttp.WriteError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	h.cookieManager.SetAuthCookie(w, newAccessToken, h.accessTokenTTL)
	zhttp.WriteJSON(w, http.StatusOK, TokenRefreshResponse{
		AccessToken: newAccessToken,
		ExpiresIn:   int64(h.accessTokenTTL.Seconds()),
	})
}

func (h *Handlers) setAuthCookies(w http.ResponseWriter, response LoginResponse) {
	h.cookieManager.SetAuthCookie(w, response.Tokens.AccessToken, h.accessTokenTTL)
	h.cookieManager.SetRefreshCookie(w, response.Tokens.RefreshToken, h.refreshTokenTTL)
}

func (h *Handlers) handleAuthError(w http.ResponseWriter, err error, action string) {
	slog.Error(action, "error", err)
	zhttp.WriteError(w, http.StatusUnauthorized, action)
}

// contextKey is used for context values.
type contextKey string

const (
	userIDKey contextKey = "userID"
	userKey   contextKey = "user"
)

// UserIDFromContext extracts the authenticated user ID from the context.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	userID, ok := ctx.Value(userIDKey).(uuid.UUID)
	return userID, ok
}

// UserFromContext extracts the authenticated user claims from context.
func UserFromContext(ctx context.Context) (*Claims, bool) {
	user, ok := ctx.Value(userKey).(*Claims)
	return user, ok
}

// SetUser attaches user claims to the context.
func SetUser(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, userKey, claims)
}

// SetUserID attaches a user ID to the context.
func SetUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}
