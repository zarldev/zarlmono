package zhttp_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

func TestNewServerAppliesSafeDefaults(t *testing.T) {
	t.Parallel()

	h := http.NewServeMux()
	srv := zhttp.NewServer("127.0.0.1:0", h)

	if srv.Addr != "127.0.0.1:0" {
		t.Fatalf("Addr = %q, want 127.0.0.1:0", srv.Addr)
	}
	if srv.Handler != h {
		t.Fatal("Handler was not preserved")
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout = %s, want non-zero", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout <= 0 {
		t.Fatalf("ReadTimeout = %s, want non-zero", srv.ReadTimeout)
	}
	if srv.WriteTimeout <= 0 {
		t.Fatalf("WriteTimeout = %s, want non-zero", srv.WriteTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %s, want non-zero", srv.IdleTimeout)
	}
}

func TestNewServerOptionsOverrideDefaults(t *testing.T) {
	t.Parallel()

	srv := zhttp.NewServer(
		":0",
		http.NotFoundHandler(),
		zhttp.WithServerReadHeaderTimeout(time.Second),
		zhttp.WithServerReadTimeout(2*time.Second),
		zhttp.WithServerWriteTimeout(3*time.Second),
		zhttp.WithServerIdleTimeout(4*time.Second),
	)

	if srv.ReadHeaderTimeout != time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 1s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 2*time.Second {
		t.Fatalf("ReadTimeout = %s, want 2s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 3*time.Second {
		t.Fatalf("WriteTimeout = %s, want 3s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 4*time.Second {
		t.Fatalf("IdleTimeout = %s, want 4s", srv.IdleTimeout)
	}
}
