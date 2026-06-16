package auth

import (
	"net/http"
	"time"
)

// CookieManager handles cookie-based authentication.
type CookieManager struct {
	secure   bool
	sameSite http.SameSite
	domain   string
	httpOnly bool
}

// NewCookieManager creates a new cookie manager. SameSite defaults to
// Lax (Strict in production) — Lax is the floor recommended by
// http.CrossOriginProtection's defence-in-depth pairing.
func NewCookieManager(secure bool, domain string) *CookieManager {
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteStrictMode
	}
	return &CookieManager{
		secure:   secure,
		sameSite: sameSite,
		domain:   domain,
		httpOnly: true,
	}
}

// SetAuthCookie sets the JWT token in an httpOnly cookie.
func (c *CookieManager) SetAuthCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     "/",
		Domain:   c.domain,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: c.httpOnly,
		Secure:   c.secure,
		SameSite: c.sameSite,
	})
}

// SetRefreshCookie sets the refresh token in an httpOnly cookie scoped
// to /api/refresh so it never travels to other endpoints.
func (c *CookieManager) SetRefreshCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    token,
		Path:     "/api/refresh",
		Domain:   c.domain,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: c.httpOnly,
		Secure:   c.secure,
		SameSite: c.sameSite,
	})
}

// ClearAuthCookies removes both auth cookies. Browsers identify
// cookies by (name, domain, path), so the delete-cookie response
// MUST match the path the cookie was set with. auth_token lives at
// "/" (SetAuthCookie) and refresh_token lives at "/api/refresh"
// (SetRefreshCookie). Earlier this helper cleared both at "/"
// which silently left the refresh cookie in place — logout
// appeared to work but the refresh credential survived.
func (c *CookieManager) ClearAuthCookies(w http.ResponseWriter) {
	clears := []struct {
		name string
		path string
	}{
		{"auth_token", "/"},
		{"refresh_token", "/api/refresh"},
	}
	for _, ck := range clears {
		http.SetCookie(w, &http.Cookie{
			Name:     ck.name,
			Value:    "",
			Path:     ck.path,
			Domain:   c.domain,
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   c.secure,
			SameSite: c.sameSite,
		})
	}
}

// GetTokenFromCookie extracts the auth token from cookies.
func GetTokenFromCookie(r *http.Request) (string, error) {
	cookie, err := r.Cookie("auth_token")
	if err != nil {
		return "", err
	}
	return cookie.Value, nil
}
