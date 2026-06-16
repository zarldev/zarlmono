package middleware

import (
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CrossOrigin returns the stdlib http.CrossOriginProtection middleware
// for the modern Sec-Fetch-Site / Origin-vs-Host CSRF strategy. Pair
// with SameSite=Lax cookies (older browsers) for full coverage.
//
// The token-based synchronizer pattern (CSRFManager + X-CSRF-Token
// header) was retired in favour of this approach — see Edwards's
// "A Modern Approach to Preventing CSRF in Go".
func CrossOrigin() func(http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	return cop.Handler
}

// CORSOptions configures cross-origin resource sharing. The zero value
// denies every cross-origin request (no CORS headers emitted) — callers
// must opt into an origin policy explicitly rather than inherit a
// permissive default.
type CORSOptions struct {
	// AllowedOrigins is the exact-match origin allowlist. The special
	// value "*" allows any origin, but per the Fetch spec it is honoured
	// ONLY when AllowCredentials is false — a wildcard can never be
	// combined with credentials, so "*" + AllowCredentials degrades to
	// "listed origins only".
	AllowedOrigins []string
	// AllowedMethods / AllowedHeaders populate the preflight response.
	AllowedMethods []string
	AllowedHeaders []string
	// AllowCredentials permits cookies / Authorization on cross-origin
	// requests. It forces per-origin reflection (the response echoes the
	// caller's Origin, never "*") and only ever applies to an explicitly
	// listed origin — the combination a cookie-authenticated API needs.
	AllowCredentials bool
	// MaxAge caps how long a browser may cache the preflight result.
	MaxAge time.Duration
}

// CORSWithOptions returns CORS middleware driven by an explicit origin
// allowlist. This is the form a credentialed (cookie / bearer) API
// should use: it reflects only allowlisted origins, adds Vary: Origin so
// shared caches don't leak one origin's policy to another, and never
// emits "*" together with Access-Control-Allow-Credentials.
func CORSWithOptions(opts CORSOptions) func(http.Handler) http.Handler {
	allowAny := slices.Contains(opts.AllowedOrigins, "*")
	allowed := make(map[string]struct{}, len(opts.AllowedOrigins))
	for _, o := range opts.AllowedOrigins {
		allowed[o] = struct{}{}
	}
	methods := strings.Join(opts.AllowedMethods, ", ")
	headers := strings.Join(opts.AllowedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowOrigin := resolveAllowOrigin(r.Header.Get("Origin"), allowed, allowAny, opts.AllowCredentials)
			if allowOrigin != "" {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", allowOrigin)
				if allowOrigin != "*" {
					// The decision depended on the request's Origin, so a
					// shared cache must key on it.
					h.Add("Vary", "Origin")
					if opts.AllowCredentials {
						h.Set("Access-Control-Allow-Credentials", "true")
					}
				}
				if methods != "" {
					h.Set("Access-Control-Allow-Methods", methods)
				}
				if headers != "" {
					h.Set("Access-Control-Allow-Headers", headers)
				}
				if opts.MaxAge > 0 {
					h.Set("Access-Control-Max-Age", strconv.Itoa(int(opts.MaxAge.Seconds())))
				}
			}

			// A CORS preflight is an OPTIONS carrying
			// Access-Control-Request-Method; answer it here. A bare OPTIONS
			// falls through to the application.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowOrigin == "" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// resolveAllowOrigin picks the Access-Control-Allow-Origin value for a
// request, or "" to emit no CORS headers at all (deny). With
// credentials, only an explicitly listed origin is reflected — never the
// wildcard.
func resolveAllowOrigin(origin string, allowed map[string]struct{}, allowAny, credentials bool) string {
	if origin != "" {
		if _, ok := allowed[origin]; ok {
			return origin
		}
		if allowAny && !credentials {
			return "*"
		}
		return ""
	}
	// No Origin header (same-origin or non-browser client): a public
	// wildcard API still advertises "*"; a credentialed/allowlist policy
	// emits nothing.
	if allowAny && !credentials {
		return "*"
	}
	return ""
}

// CORS is a convenience wrapper for a PUBLIC, credential-free API: it
// allows any origin and the common verbs/headers, but emits no
// Access-Control-Allow-Credentials. Do NOT use it for a cookie- or
// bearer-authenticated service — a wildcard origin lets any site script
// the API on a victim's behalf. Reach for [CORSWithOptions] with an
// explicit AllowedOrigins allowlist and AllowCredentials there.
func CORS(next http.Handler) http.Handler {
	return CORSWithOptions(CORSOptions{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization", "X-CSRF-Token"},
	})(next)
}

// JSONContentType sets JSON content type.
func JSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// RequestLogging logs HTTP requests.
func RequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a custom response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Log request start
		startTime := time.Now()
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()[:8]
		}

		slog.DebugContext(r.Context(), "request started",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr)

		// Process request
		next.ServeHTTP(rw, r)

		// Log request completion
		duration := time.Since(startTime)
		slog.DebugContext(r.Context(), "request completed",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"duration", duration,
			"status", rw.statusCode)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
