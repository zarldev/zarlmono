package zhttp

import (
	"net/http"
	"time"

	"github.com/zarldev/zarlmono/zkit/options"
)

const (
	defaultServerReadHeaderTimeout = 10 * time.Second
	defaultServerReadTimeout       = 30 * time.Second
	defaultServerWriteTimeout      = 30 * time.Second
	defaultServerIdleTimeout       = 90 * time.Second
)

// ServerConfig holds safe defaults for constructing an HTTP server.
// Values map directly to fields on [http.Server].
type ServerConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// DefaultServerConfig returns conservative timeout defaults for small
// JSON/API servers. Callers can override individual fields with options.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		ReadHeaderTimeout: defaultServerReadHeaderTimeout,
		ReadTimeout:       defaultServerReadTimeout,
		WriteTimeout:      defaultServerWriteTimeout,
		IdleTimeout:       defaultServerIdleTimeout,
	}
}

// NewServer returns an [http.Server] with the package's safe timeout
// defaults applied. It returns the concrete server so callers can still
// configure TLS, BaseContext, ConnContext, ErrorLog, and shutdown policy.
func NewServer(addr string, handler http.Handler, opts ...options.Option[ServerConfig]) *http.Server {
	cfg := DefaultServerConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
}

// WithServerReadHeaderTimeout sets how long the server waits for request
// headers. Keep non-zero for public or loopback servers alike; it prevents
// slowloris-style connections from occupying resources indefinitely.
func WithServerReadHeaderTimeout(d time.Duration) options.Option[ServerConfig] {
	return func(c *ServerConfig) { c.ReadHeaderTimeout = d }
}

// WithServerReadTimeout sets the total read timeout for request headers
// and body. Use 0 only for endpoints that intentionally stream uploads.
func WithServerReadTimeout(d time.Duration) options.Option[ServerConfig] {
	return func(c *ServerConfig) { c.ReadTimeout = d }
}

// WithServerWriteTimeout sets how long response writes may take. Use 0
// only for endpoints that intentionally stream responses indefinitely.
func WithServerWriteTimeout(d time.Duration) options.Option[ServerConfig] {
	return func(c *ServerConfig) { c.WriteTimeout = d }
}

// WithServerIdleTimeout sets how long keep-alive connections remain idle.
func WithServerIdleTimeout(d time.Duration) options.Option[ServerConfig] {
	return func(c *ServerConfig) { c.IdleTimeout = d }
}
