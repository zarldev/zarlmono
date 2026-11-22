package litellm_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

func TestModelsOrdinaryKey(t *testing.T) {
	t.Parallel()
	var count atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/gateway/v1/models" || r.URL.RawQuery != "" {
			t.Errorf("unexpected discovery endpoint: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ordinary-test-key" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("Anthropic-Version") != "" || r.Header.Get("Client_version") != "" {
			t.Error("discovery authentication or negotiation headers changed")
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":" Company/Production Alias-V7 "},{"id":"gpt-5.6"},{"id":" Company/Production Alias-V7 "},{"id":"GPT-5.6"}]}`)
	}))
	t.Cleanup(server.Close)
	provider := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL+"/gateway/"), litellm.WithProtocol(llm.ModelProtocols.PROTOCOLMESSAGES))
	want := []llm.ModelID{" Company/Production Alias-V7 ", "gpt-5.6", "GPT-5.6"}
	first, err := provider.Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first, want) {
		t.Fatalf("IDs = %q", first)
	}
	first[0] = "mutated"
	second, err := provider.Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(second, want) || count.Load() != 2 {
		t.Fatalf("independent models = %q; requests = %d", second, count.Load())
	}
}

func TestModelsEnvelopes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body string
		err        error
	}{
		{name: "empty list", body: `{"data":[]}`},
		{name: "missing data", body: `{}`, err: litellm.ErrModelsResponse},
		{name: "null envelope", body: `null`, err: litellm.ErrModelsResponse},
		{name: "null list", body: `{"data":null}`, err: litellm.ErrModelsResponse},
		{name: "list envelope", body: `[]`, err: litellm.ErrModelsResponse},
		{name: "object data", body: `{"data":{}}`, err: litellm.ErrModelsResponse},
		{name: "null row", body: `{"data":[null]}`, err: litellm.ErrModelsResponse},
		{name: "empty ID", body: `{"data":[{"id":""}]}`, err: litellm.ErrModelsResponse},
		{name: "missing ID", body: `{"data":[{}]}`, err: litellm.ErrModelsResponse},
		{name: "null ID", body: `{"data":[{"id":null}]}`, err: litellm.ErrModelsResponse},
		{name: "number ID", body: `{"data":[{"id":3}]}`, err: litellm.ErrModelsResponse},
		{name: "space ID", body: `{"data":[{"id":"  "}]}`, err: litellm.ErrModelsResponse},
		{name: "control ID", body: `{"data":[{"id":"a\u0000b"}]}`, err: litellm.ErrModelsResponse},
		{name: "trailing envelope", body: `{"data":[]} {}`, err: litellm.ErrModelsResponse},
		{name: "truncated", body: `{"data":[`, err: litellm.ErrModelsResponse},
		{name: "unpaired high surrogate", body: `{"data":[{"id":"a\ud800b"},{"id":"a\ufffdb"}]}`, err: litellm.ErrModelsResponse},
		{name: "high surrogate before non-low escape", body: `{"data":[{"id":"a\ud800\u0041"}]}`, err: litellm.ErrModelsResponse},
		{name: "two high surrogates", body: `{"data":[{"id":"a\ud800\ud800"}]}`, err: litellm.ErrModelsResponse},
		{name: "unpaired low surrogate", body: `{"data":[{"id":"a\udc00b"}]}`, err: litellm.ErrModelsResponse},
		{name: "invalid UTF8", body: "{\"data\":[{\"id\":\"\xff\"}]}", err: litellm.ErrModelsResponse},
		{name: "oversized", body: `{"data":[{"id":"` + strings.Repeat("a", 8<<20) + `"}]}`, err: litellm.ErrModelsResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := &trackedBody{Reader: strings.NewReader(test.body)}
			provider := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: r}, nil
			})}))
			ids, err := provider.Models(t.Context())
			if !errors.Is(err, test.err) {
				t.Fatalf("Models error = %v, want %v", err, test.err)
			}
			if !body.closed.Load() {
				t.Error("response body not closed")
			}
			if test.err == nil && (ids == nil || len(ids) != 0) {
				t.Errorf("empty data = %#v", ids)
			}
			if test.err != nil && ids != nil {
				t.Errorf("partial invalid model list = %q", ids)
			}
		})
	}
}

func TestModelsHTTPStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		status int
		err    error
	}{
		{status: http.StatusUnauthorized, err: llm.ErrInvalidAPIKey},
		{status: http.StatusForbidden, err: llm.ErrInvalidAPIKey},
		{status: http.StatusTooManyRequests, err: llm.ErrRateLimitExceeded},
		{status: http.StatusServiceUnavailable, err: llm.ErrProviderUnavailable},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()
			const secret = "PRIVATE-RESPONSE-SECRET-CANARY"
			body := &trackedBody{Reader: strings.NewReader(secret)}
			provider := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: body, Header: http.Header{"Sensitive-Header": {secret}}, Request: r}, nil
			})}))
			_, err := provider.Models(t.Context())
			var status *litellm.HTTPError
			if !errors.Is(err, test.err) || !errors.As(err, &status) || status.StatusCode != test.status {
				t.Fatalf("HTTP error = %v", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "ordinary-test-key") {
				t.Error("secret in HTTP diagnostic")
			}
			if !body.closed.Load() {
				t.Error("error response body not closed")
			}
		})
	}
}

func TestModelsCancellation(t *testing.T) {
	t.Parallel()
	cause := errors.New("stop model discovery")
	ctx, cancel := context.WithCancelCause(t.Context())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[`)
		w.(http.Flusher).Flush()
		cancel(cause)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	provider := litellm.NewProvider("ordinary-test-key", litellm.WithBaseURL(server.URL))
	_, err := provider.Models(ctx)
	if !errors.Is(err, cause) {
		t.Fatalf("cancellation = %v", err)
	}
}

type trackedBody struct {
	*strings.Reader
	closed atomic.Bool
}

func (b *trackedBody) Close() error { b.closed.Store(true); return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelsUnicodeIdentity(t *testing.T) {
	t.Parallel()
	provider := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"a\ud83d\ude00b"},{"id":"a😀b"},{"id":"a\ufffdb"},{"id":"a�b"},{"id":"a\\ud800b"}]}`)), Request: r}, nil
	})}))
	ids, err := provider.Models(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.ModelID{"a😀b", "a�b", `a\ud800b`}
	if !slices.Equal(ids, want) {
		t.Fatalf("IDs = %q, want %q", ids, want)
	}
}
