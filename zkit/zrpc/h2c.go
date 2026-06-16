// Package zrpc provides ConnectRPC server helpers — common interceptors
// (logging, recovery, request-ID) and the HTTP/2-cleartext wrapper every
// Connect server needs.
//
// Typical wiring:
//
//	mux := http.NewServeMux()
//	mux.Handle(myservicev1connect.NewMyServiceHandler(server,
//	    connect.WithInterceptors(
//	        middleware.RequestID(),
//	        middleware.Logging(slog.Default()),
//	        middleware.Recovery(slog.Default()),
//	    ),
//	))
//	srv := &http.Server{Addr: ":8080", Handler: zrpc.H2CHandler(mux)}
//	srv.ListenAndServe()
//
// Each piece is independently useful — the middleware sub-package
// works with any ConnectRPC handler, and H2CHandler wraps any
// http.Handler regardless of whether ConnectRPC is involved.
package zrpc

import (
	"net/http"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// H2CHandler wraps an http.Handler with HTTP/2-cleartext support. Every
// Connect server that wants to serve gRPC-style HTTP/2 traffic over a
// plain TCP listener (no TLS) needs this.
//
// Use TLS in production; H2CHandler is for development and inside-
// cluster traffic where TLS is terminated upstream.
//
// Deprecated: h2c.NewHandler is deprecated in the upstream library.
// Prefer using http.Server with the Protocols field set:
//
//	srv := &http.Server{
//	    Addr:      ":8080",
//	    Handler:   mux,
//	    Protocols: &http2.Server{},
//	}
func H2CHandler(handler http.Handler) http.Handler {
	return h2c.NewHandler(handler, &http2.Server{})
}
