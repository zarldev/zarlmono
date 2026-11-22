package litellm_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/providertest"
)

const gatewaySecret = "PRIVATE-GATEWAY-SECRET-CANARY"

func TestInBandErrors(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		for _, test := range []struct {
			name, code, kind string
			status           int
			want             error
		}{
			{name: "auth", code: "invalid_api_key", kind: "authentication_error", status: 401, want: llm.ErrInvalidAPIKey},
			{name: "rate", code: "rate_limit_exceeded", kind: "rate_limit_error", status: 429, want: llm.ErrRateLimitExceeded},
		} {
			t.Run(fixture.protocol.String()+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				body := fmt.Sprintf(`{"error":{"code":%q,"type":%q,"message":%q}}`, test.code, test.kind, gatewaySecret)
				prefix := ""
				switch fixture.protocol {
				case llm.ModelProtocols.PROTOCOLRESPONSES:
					body = fmt.Sprintf(`{"type":"error","code":%q,"message":%q}`, test.code, gatewaySecret)
				case llm.ModelProtocols.PROTOCOLMESSAGES:
					prefix = "event: error\n"
					body = fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, test.kind, gatewaySecret)
				case llm.ModelProtocols.PROTOCOLGENERATECONTENT:
					body = fmt.Sprintf(`{"error":{"code":%d,"status":%q,"message":%q}}`, test.status, map[int]string{401: "UNAUTHENTICATED", 429: "RESOURCE_EXHAUSTED"}[test.status], gatewaySecret)
				}
				p := errorProvider(fixture.protocol, "text/event-stream", prefix+"data: "+body+"\n\n", http.StatusOK)
				err := terminalError(t, p.Complete(t.Context(), providertest.SimpleRequest("hi")))
				assertSafeError(t, err)
				if !errors.Is(err, test.want) {
					t.Errorf("identity = %v, want %v", err, test.want)
				}
			})
		}
	}
}

func TestCodecDiagnosticsSanitized(t *testing.T) {
	t.Parallel()
	for _, stream := range []bool{true, false} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			t.Parallel()
			body, contentType := `{"candidates":`+gatewaySecret, "application/json"
			if stream {
				body, contentType = "data: "+body+"\n\n", "text/event-stream"
			}
			p := errorProvider(llm.ModelProtocols.PROTOCOLGENERATECONTENT, contentType, body, http.StatusOK)
			req := providertest.SimpleRequest("hi")
			req.Stream = stream
			assertSafeError(t, terminalError(t, p.Complete(t.Context(), req)))
		})
	}
}

func TestGatewayHTTPAndTransportErrors(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			for _, status := range []int{401, 429, 503} {
				p := errorProvider(fixture.protocol, "application/json", gatewaySecret, status)
				err := terminalError(t, p.Complete(t.Context(), providertest.SimpleRequest("hi")))
				assertSafeError(t, err)
				want := map[int]error{401: llm.ErrInvalidAPIKey, 429: llm.ErrRateLimitExceeded, 503: llm.ErrProviderUnavailable}[status]
				if !errors.Is(err, want) {
					t.Errorf("status identity = %v, want %v", err, want)
				}
				var statusErr *litellm.HTTPError
				if !errors.As(err, &statusErr) || statusErr.StatusCode != status {
					t.Errorf("status = %v", err)
				}
			}
			p := litellm.NewProvider("ordinary-test-key", litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New(gatewaySecret) })}))
			assertSafeError(t, terminalError(t, p.Complete(t.Context(), providertest.SimpleRequest("hi"))))
		})
	}
}

func TestCallerCancellationCauseRetained(t *testing.T) {
	t.Parallel()
	for _, fixture := range fixtures() {
		t.Run(fixture.protocol.String(), func(t *testing.T) {
			t.Parallel()
			cause := errors.New("caller cause: " + gatewaySecret)
			ctx, cancel := context.WithCancelCause(t.Context())
			p := litellm.NewProvider("ordinary-test-key", litellm.WithProtocol(fixture.protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { cancel(cause); return nil, ctx.Err() })}))
			err := terminalError(t, p.Complete(ctx, providertest.SimpleRequest("hi")))
			if !errors.Is(err, cause) {
				t.Errorf("cause = %v", err)
			}
		})
	}
}

func errorProvider(protocol llm.ModelProtocol, contentType, body string, status int) *litellm.Provider {
	return litellm.NewProvider("ordinary-test-key", litellm.WithProtocol(protocol), litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}))
}

func terminalError(t *testing.T, stream llm.CompletionStream) error {
	t.Helper()
	var terminal error
	for chunk, err := range stream {
		if terminal != nil {
			t.Fatal("yield after terminal error")
		}
		if err != nil {
			terminal = err
			if chunk.Content != "" || len(chunk.ToolCalls) != 0 {
				t.Error("terminal error has output")
			}
		}
	}
	if terminal == nil {
		t.Fatal("missing terminal error")
	}
	return terminal
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), gatewaySecret) || strings.Contains(err.Error(), "ordinary-test-key") {
		t.Errorf("unsafe diagnostic: %v", err)
	}
}

func TestResponsesSafeRateDetails(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, code, message string
		want                error
		delay               time.Duration
		permanent           bool
	}{
		{name: "retry timing", code: "rate_limit_exceeded", message: gatewaySecret + " Rate limit reached. Please try again in 2s.", want: llm.ErrRateLimitExceeded, delay: 2 * time.Second},
		{name: "permanent quota", code: "insufficient_quota", message: gatewaySecret, want: llm.ErrRateLimitExceeded, permanent: true},
		{name: "message is not classification", code: "unknown_gateway_error", message: gatewaySecret + " invalid_api_key rate limit exceeded", want: litellm.ErrCompletion},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := fmt.Sprintf("data: {\"type\":\"error\",\"code\":%q,\"message\":%q}\n\n", test.code, test.message)
			p := errorProvider(llm.ModelProtocols.PROTOCOLRESPONSES, "text/event-stream", body, http.StatusOK)
			err := terminalError(t, p.Complete(t.Context(), providertest.SimpleRequest("hi")))
			assertSafeError(t, err)
			if !errors.Is(err, test.want) {
				t.Fatalf("identity = %v, want %v", err, test.want)
			}
			var rate *llm.RateLimitError
			if errors.Is(test.want, llm.ErrRateLimitExceeded) {
				if !errors.As(err, &rate) || rate.Permanent != test.permanent || rate.RetryAfter != test.delay || rate.Retryable == test.permanent {
					t.Fatalf("rate details = %+v", rate)
				}
			} else if errors.As(err, &rate) {
				t.Fatal("prose misclassified as rate limit")
			}
		})
	}
}

func TestChatStructuredTypeFallback(t *testing.T) {
	t.Parallel()
	p := errorProvider(llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, "text/event-stream", `data: {"error":{"code":"gateway_specific","type":"authentication_error","message":"`+gatewaySecret+`"}}`+"\n\n", http.StatusOK)
	err := terminalError(t, p.Complete(t.Context(), providertest.SimpleRequest("hi")))
	assertSafeError(t, err)
	if !errors.Is(err, llm.ErrInvalidAPIKey) {
		t.Fatalf("structured type = %v", err)
	}
}

func TestModelsDiagnosticsSanitized(t *testing.T) {
	t.Parallel()
	provider := litellm.NewProvider("ordinary-test-key", litellm.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New(gatewaySecret) })}))
	_, err := provider.Models(t.Context())
	if !errors.Is(err, litellm.ErrTransport) {
		t.Fatalf("Models transport = %v", err)
	}
	assertSafeError(t, err)
}
