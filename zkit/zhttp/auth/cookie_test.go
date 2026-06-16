package auth_test

import (
	"net/http/httptest"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zhttp/auth"
)

// Regression: ClearAuthCookies must emit a delete-cookie response
// at the SAME path the cookie was set with. Earlier the helper
// cleared both cookies at "/" which left refresh_token (set at
// "/api/refresh") in place — logout looked successful but the
// refresh credential survived. Browser cookies are identified by
// (name, domain, path); a path-mismatched expiry is silently ignored.
func TestClearAuthCookies_PathsMatchSetPaths(t *testing.T) {
	t.Parallel()
	mgr := auth.NewCookieManager(true, "")

	w := httptest.NewRecorder()
	mgr.ClearAuthCookies(w)

	cookies := w.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("ClearAuthCookies emitted %d cookies, want 2", len(cookies))
	}
	want := map[string]string{
		"auth_token":    "/",
		"refresh_token": "/api/refresh",
	}
	for _, c := range cookies {
		if c.MaxAge != -1 {
			t.Errorf("cookie %q MaxAge = %d, want -1 (delete)", c.Name, c.MaxAge)
		}
		expectPath, ok := want[c.Name]
		if !ok {
			t.Errorf("unexpected cookie name in ClearAuthCookies: %q", c.Name)
			continue
		}
		if c.Path != expectPath {
			t.Errorf("cookie %q cleared at path %q, want %q (must match SetRefreshCookie / SetAuthCookie)",
				c.Name, c.Path, expectPath)
		}
	}
}
