package litellm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ErrOrigin identifies a request outside the configured gateway origin.
var ErrOrigin = errors.New("litellm request outside gateway origin")

// ErrTransport identifies a gateway transport failure without retaining private
// transport diagnostics. Caller cancellation causes are preserved instead.
var ErrTransport = errors.New("litellm transport error")

// HTTPError reports a gateway HTTP status without retaining response bodies,
// URLs, credentials, or raw headers. All non-2xx responses, including redirects,
// are terminal; no redirect is followed, even within the configured origin.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string { return fmt.Sprintf("litellm HTTP status %d", e.StatusCode) }

// Is maps authentication failures to llm.ErrInvalidAPIKey, rate limits to
// llm.ErrRateLimitExceeded, and server failures to llm.ErrProviderUnavailable.
func (e *HTTPError) Is(target error) bool {
	switch {
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden:
		return target == llm.ErrInvalidAPIKey
	case e.StatusCode == http.StatusTooManyRequests:
		return target == llm.ErrRateLimitExceeded
	case e.StatusCode >= http.StatusInternalServerError:
		return target == llm.ErrProviderUnavailable
	default:
		return false
	}
}

func (p *Provider) client(protocol llm.ModelProtocol) *http.Client {
	return p.requestClient(protocol, nil)
}

func (p *Provider) requestClient(protocol llm.ModelProtocol, execution *executionState) *http.Client {
	client := http.Client{}
	if p.httpClient != nil {
		client = *p.httpClient
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = gatewayTransport{baseURL: p.baseURL, apiKey: p.apiKey, protocol: protocol, next: transport, execution: execution}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

type gatewayTransport struct {
	baseURL   string
	apiKey    string
	protocol  llm.ModelProtocol
	next      http.RoundTripper
	execution *executionState
}

func (t gatewayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin, err := url.Parse(t.baseURL)
	if err != nil || origin.Scheme != req.URL.Scheme || origin.Host != req.URL.Host {
		return nil, ErrOrigin
	}
	local := req.Clone(req.Context())
	local.Header.Del("Authorization")
	local.Header.Del("X-Api-Key")
	local.Header.Del("X-Goog-Api-Key")
	if t.execution != nil {
		local.Header.Del("X-Litellm-Session-Id")
		if t.execution.session != "" {
			local.Header.Set("X-Litellm-Session-Id", string(t.execution.session))
			local.Header.Del("X-Litellm-Trace-Id")
		}
	}
	// SDK defaults may consult ambient credentials; the gateway always receives
	// exactly the configured key in the selected protocol's documented header.
	if t.protocol == llm.ModelProtocols.PROTOCOLMESSAGES {
		local.Header.Set("X-Api-Key", t.apiKey)
	} else {
		local.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	response, err := t.next.RoundTrip(local)
	if err != nil {
		if cause := context.Cause(req.Context()); cause != nil {
			return nil, cause
		}
		return nil, ErrTransport
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		return nil, &HTTPError{StatusCode: response.StatusCode}
	}
	if t.execution != nil {
		t.execution.observe(response.Header)
	}
	return response, nil
}
