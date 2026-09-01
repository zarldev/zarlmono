package codex_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
	"github.com/zarldev/zarlmono/zkit/oauth/codex"
)

func TestAwaitCallbackRejectsStateMismatch(t *testing.T) {
	svc := openTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		_, err := codex.AwaitCallback(ctx, svc, openaicodex.AuthorizationFlow{
			State: "state-good",
			PKCE:  openaicodex.PKCE{Verifier: "unused"},
		})
		errCh <- err
	}()

	resp := getCallback(t, ctx, "?code=x&state=state-bad")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	select {
	case err := <-errCh:
		t.Fatalf("state mismatch unblocked callback waiter: %v", err)
	default:
	}
	cancel()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "timed out waiting") {
		t.Errorf("AwaitCallback() error = %v, want canceled wait error", err)
	}
}

func TestAwaitCallbackRejectsMissingCode(t *testing.T) {
	svc := openTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		_, err := codex.AwaitCallback(ctx, svc, openaicodex.AuthorizationFlow{
			State: "state-good",
			PKCE:  openaicodex.PKCE{Verifier: "unused"},
		})
		errCh <- err
	}()

	resp := getCallback(t, ctx, "?state=state-good")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "missing authorization code") {
		t.Errorf("AwaitCallback() error = %v, want missing-code error", err)
	}
}

func getCallback(t *testing.T, ctx context.Context, query string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	url := "http://127.0.0.1:1455/auth/callback" + query
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("build callback request: %v", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			return resp
		}
		select {
		case <-ctx.Done():
			t.Fatalf("callback server did not start: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
