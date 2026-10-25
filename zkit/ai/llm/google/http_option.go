package google

import (
	"net/http"

	"github.com/zarldev/zarlmono/zkit/options"
)

// WithHTTPClient borrows the caller's SDK HTTP client without changing its
// timeout, authentication, or transport. Vertex clients must authenticate
// themselves; use WithClient for a fully configured genai client instead.
func WithHTTPClient(client *http.Client) options.Option[Provider] {
	return func(provider *Provider) { provider.httpClient = client }
}
