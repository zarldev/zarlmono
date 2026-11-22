package litellm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

// ErrCompletion identifies a terminal codec failure without exposing remote
// payloads, request URLs, credentials, or SDK diagnostics. Recognized structured
// failures additionally expose the corresponding llm error identity.
var ErrCompletion = errors.New("litellm completion error")

// completionError is the final untrusted codec boundary. Never wrap the native
// error: even innocuous SDK diagnostics can retain whole requests or payloads.
func completionError(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if status, ok := errors.AsType[*HTTPError](err); ok {
		return &HTTPError{StatusCode: status.StatusCode}
	}
	for _, identity := range []error{ErrOrigin, ErrTransport, llm.ErrInvalidAPIKey, llm.ErrRateLimitExceeded, llm.ErrProviderUnavailable, llm.ErrModelNotSupported, llm.ErrContextLengthExceeded, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, identity) {
			return identity
		}
	}
	if response, ok := errors.AsType[*openai.ResponsesError](err); ok {
		return structuredError(response.StatusCode, response.Code, err)
	}
	if stream, ok := errors.AsType[*ssestream.StreamError](err); ok {
		// The SDK retains the original event, not only its unsafe Error() text.
		// Bound the extra decode independently of the SDK's stream-buffer limit.
		var envelope struct {
			Error struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if len(stream.Event.Data) > 64<<10 || json.Unmarshal(stream.Event.Data, &envelope) != nil {
			return ErrCompletion
		}
		classified := structuredError(0, envelope.Error.Code, err)
		if errors.Is(classified, ErrCompletion) {
			return structuredError(0, envelope.Error.Type, err)
		}
		return classified
	}
	if api, ok := errors.AsType[*anthropicsdk.Error](err); ok {
		return structuredError(api.StatusCode, string(api.Type()), err)
	}
	if api, ok := errors.AsType[genai.APIError](err); ok {
		return structuredError(api.Code, api.Status, err)
	}
	if _, ok := errors.AsType[*llm.RateLimitError](err); ok {
		return safeRateLimit(err)
	}
	return ErrCompletion
}

func structuredError(status int, code string, err error) error {
	if status >= http.StatusBadRequest {
		httpErr := &HTTPError{StatusCode: status}
		if errors.Is(httpErr, llm.ErrRateLimitExceeded) {
			return safeRateLimit(err)
		}
		return httpErr
	}
	switch code {
	case "invalid_api_key", "authentication_error", "permission_error", "UNAUTHENTICATED", "PERMISSION_DENIED":
		return llm.ErrInvalidAPIKey
	case "rate_limit_exceeded", "rate_limit_error", "RESOURCE_EXHAUSTED":
		return safeRateLimit(err)
	case "insufficient_quota":
		return fmt.Errorf("litellm: %w: %w", llm.ErrRateLimitExceeded, &llm.RateLimitError{Message: "gateway quota exhausted", Permanent: true})
	case "server_error", "api_error", "overloaded_error", "INTERNAL", "UNAVAILABLE":
		return llm.ErrProviderUnavailable
	case "context_length_exceeded":
		return llm.ErrContextLengthExceeded
	default:
		return ErrCompletion
	}
}

func safeRateLimit(err error) error {
	rate := llm.RateLimitError{Message: "gateway rate limit", Retryable: true}
	if original, ok := errors.AsType[*llm.RateLimitError](err); ok {
		rate = *original
		rate.Message = "gateway rate limit"
	}
	return fmt.Errorf("litellm: %w: %w", llm.ErrRateLimitExceeded, &rate)
}
