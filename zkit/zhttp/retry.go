package zhttp

import (
	"net/http"
	"slices"

	"github.com/zarldev/zarlmono/zkit/options"
)

// WithRetryMethods replaces the set of methods eligible for automatic retries.
// Defaults are GET, HEAD, OPTIONS, TRACE, PUT, and DELETE. An empty method on a
// request is treated as GET, matching net/http. Pass no methods to disable
// retries. Opt in to POST or PATCH only when the endpoint is safe to repeat;
// eligibility still requires a replayable body and a retryable result.
func WithRetryMethods(methods ...string) options.Option[Client] {
	allowed := slices.Clone(methods)
	return func(c *Client) { c.retryMethods = allowed }
}

func (c *Client) canRetry(req *http.Request) bool {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	return slices.Contains(c.retryMethods, method)
}
