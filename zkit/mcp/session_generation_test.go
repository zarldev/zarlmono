package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/mcp"
)

func TestDelayedExpiredSessionDoesNotInvalidateRecoveredGeneration(t *testing.T) {
	var initializes atomic.Int32
	var mutations atomic.Int32
	secondAccepted := make(chan struct{})
	releaseSecond := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		request := decodeLifecycleRequest(t, r)
		switch request.Method {
		case "server/discover":
			writeLifecycleMethodNotFound(t, w, request)
		case "initialize":
			generation := initializes.Add(1)
			session := "first"
			if generation > 1 {
				session = "second"
			}
			w.Header().Set("Mcp-Session-Id", session)
			writeLifecycleResult(t, w, request, map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeLifecycleResult(t, w, request, map[string]any{"tools": []any{}})
		case "tools/call":
			ordinal := mutations.Add(1)
			if r.Header.Get("Mcp-Session-Id") != "first" {
				t.Error("mutating request was replayed with recovered session")
			}
			if ordinal == 1 {
				select {
				case <-secondAccepted:
				case <-r.Context().Done():
					return
				}
			} else {
				close(secondAccepted)
				select {
				case <-releaseSecond:
				case <-r.Context().Done():
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected method %s", request.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	client := mcp.NewClient(server.URL, "")
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := client.Call(ctx, "mutate", nil); firstDone <- err }()
	go func() { _, err := client.Call(ctx, "mutate", nil); secondDone <- err }()
	// Either caller can be the fast response. Observe one failure while the
	// other generation-one response remains held by the synthetic peer.
	select {
	case err := <-firstDone:
		if err == nil {
			t.Error("expired mutation succeeded")
		}
		firstDone = nil
	case err := <-secondDone:
		if err == nil {
			t.Error("expired mutation succeeded")
		}
		secondDone = nil
	case <-t.Context().Done():
		t.Fatal("mutations did not reach peer")
	}
	if _, err := client.Discover(t.Context()); err != nil {
		close(releaseSecond)
		t.Fatal(err)
	}
	close(releaseSecond)
	if firstDone != nil {
		if err := <-firstDone; err == nil {
			t.Error("delayed expired mutation succeeded")
		}
	}
	if secondDone != nil {
		if err := <-secondDone; err == nil {
			t.Error("delayed expired mutation succeeded")
		}
	}
	if _, err := client.Discover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if initializes.Load() != 2 || mutations.Load() != 2 {
		t.Fatalf("session recovery repeated or mutation replayed: initialize=%d calls=%d", initializes.Load(), mutations.Load())
	}
}
