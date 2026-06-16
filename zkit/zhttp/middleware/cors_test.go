package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zhttp/middleware"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// The bare CORS helper is the public, credential-free form: wildcard
// origin, and crucially NO Access-Control-Allow-Credentials (the
// combination that would let any site script a cookie-auth'd API).
func TestCORS_WildcardWithoutCredentials(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")

	middleware.CORS(okHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin = %q, want \"*\"", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials = %q, want empty (wildcard must never carry credentials)", got)
	}
}

func TestCORSWithOptions_AllowlistReflectsAndVaries(t *testing.T) {
	t.Parallel()
	mw := middleware.CORSWithOptions(middleware.CORSOptions{
		AllowedOrigins:   []string{"https://app.example.com"},
		AllowedMethods:   []string{"GET", "POST"},
		AllowCredentials: true,
	})

	t.Run("listed origin is reflected with credentials", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		mw(okHandler()).ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Errorf("Allow-Origin = %q, want the reflected origin", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Allow-Credentials = %q, want \"true\"", got)
		}
		if got := rec.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary = %q, want \"Origin\"", got)
		}
	})

	t.Run("unlisted origin gets nothing", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://evil.example")
		mw(okHandler()).ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Allow-Origin = %q, want empty for an unlisted origin", got)
		}
	})
}

// "*" combined with credentials must degrade to listed-origins-only: a
// wildcard is never emitted alongside credentials, and an unlisted
// origin is denied even though "*" is present.
func TestCORSWithOptions_WildcardWithCredentialsDegrades(t *testing.T) {
	t.Parallel()
	mw := middleware.CORSWithOptions(middleware.CORSOptions{
		AllowedOrigins:   []string{"*", "https://app.example.com"},
		AllowCredentials: true,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	mw(okHandler()).ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty — wildcard+credentials must not allow arbitrary origins", got)
	}

	// The explicitly listed origin still works.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Origin", "https://app.example.com")
	mw(okHandler()).ServeHTTP(rec2, req2)
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("Allow-Origin = %q, want the listed origin reflected", got)
	}
}

func TestCORSWithOptions_Preflight(t *testing.T) {
	t.Parallel()
	mw := middleware.CORSWithOptions(middleware.CORSOptions{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{"GET", "POST"},
	})

	t.Run("allowed origin preflight is answered 204", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		nextCalled := false
		mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })).ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want 204", rec.Code)
		}
		if nextCalled {
			t.Error("preflight should be terminal, not forwarded to the app")
		}
	})

	t.Run("disallowed origin preflight is forbidden", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", "POST")
		mw(okHandler()).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 for a disallowed preflight", rec.Code)
		}
	})

	t.Run("bare OPTIONS falls through to the app", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "https://app.example.com")
		nextCalled := false
		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusTeapot)
		})).ServeHTTP(rec, req)

		if !nextCalled {
			t.Error("a non-preflight OPTIONS should reach the app")
		}
		if rec.Code != http.StatusTeapot {
			t.Errorf("status = %d, want the app's 418", rec.Code)
		}
	})
}
