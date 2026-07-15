package openaicodex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/options"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

const (
	defaultBaseURL = "https://chatgpt.com/backend-api"
	defaultModel   = "gpt-5.6"
)

// Provider implements llm.Provider against OpenAI's Codex backend
// using a ChatGPT-Plus/Pro OAuth credential. The provider is stateless
// across requests; per-request state lives in the SSE parser.
type Provider struct {
	tokens  TokenSource
	client  *zhttp.Client
	baseURL string
	model   string

	// defaultEffort is the user-pinned reasoning effort applied to
	// requests where the resolved model leaves it unspecified (the
	// usual case now that the picker exposes base models only and
	// effort is a separate setting). Per-request `reasoning_effort`
	// in CompletionRequest.Options always wins; this is the floor.
	defaultEffort reasoningEffort
}

// newCodexClient builds the [zhttp.Client] the provider drives. No
// whole-request timeout — Codex SSE generations can run for minutes,
// and [http.Client.Timeout] covers the streaming body read; ctx is
// the lifetime governor. Transport-level dial/TLS/header/idle
// timeouts come from [zhttp.DefaultTransport]. Retry honors
// Retry-After on 429/5xx and replays the request body via the
// [bytes.Reader] [http.Request.GetBody] populates automatically.
func newCodexClient(policy zhttp.RetryPolicy) *zhttp.Client {
	return zhttp.NewClient(
		zhttp.WithTimeout(0),
		zhttp.WithRetryPolicy(policy),
		zhttp.WithUserAgent(originatorCodex),
	)
}

// NewProvider constructs a Codex provider. The TokenSource is the only
// required argument — without it there's no credential to send. The
// returned llm.Provider is safe for concurrent use; the underlying
// TokenSource is expected to serialise its own refresh.
func NewProvider(tokens TokenSource, opts ...options.Option[Provider]) (*Provider, error) {
	if tokens == nil {
		return nil, errors.New("openaicodex: TokenSource is required")
	}
	p := &Provider{
		tokens:  tokens,
		client:  newCodexClient(defaultRetryPolicy()),
		baseURL: defaultBaseURL,
		model:   defaultModel,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// WithModel sets the default model the provider sends. Callers can
// still override per request via CompletionRequest model handling
// (when that's added) or via Options.
func WithModel(model string) options.Option[Provider] {
	return func(p *Provider) {
		if model != "" {
			p.model = model
		}
	}
}

// WithDefaultReasoningEffort pins a default effort applied when the
// resolved model carries none and the request doesn't override.
// Empty string is a no-op (falls back to the model-name heuristic
// in defaultReasoningEffort). Valid values: "low", "medium",
// "high", "xhigh", "max" — case-sensitive to match the wire format.
func WithDefaultReasoningEffort(effort string) options.Option[Provider] {
	return func(p *Provider) {
		if effort != "" {
			p.defaultEffort = reasoningEffort(effort)
		}
	}
}

// WithBaseURL points the provider at a different Codex endpoint. The
// canonical value is the package default; this exists so tests can
// drop in an httptest server.
func WithBaseURL(baseURL string) options.Option[Provider] {
	return func(p *Provider) {
		if baseURL != "" {
			p.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return llm.LLMProviders.OPENAICODEX.String() }

// Complete is the main entry point. It does the request build, fires
// the POST, and streams SSE chunks as a native iter.Seq2 (errors as the
// second value). The Codex Responses backend is always asked for
// streaming output here even when the caller's generic
// llm.CompletionRequest has Stream=false; keeping one wire shape avoids
// a separate JSON response parser and preserves live reasoning/tool-call
// events.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	return func(yield func(llm.CompletionChunk, error) bool) {
		if err := p.run(ctx, req, yield); err != nil {
			yield(llm.CompletionChunk{Done: true, FinishReason: "error"}, err)
		}
	}, nil
}

// run does the work behind Complete. Returning a non-nil error here
// causes Complete to emit a synthetic terminal error chunk.
func (p *Provider) run(ctx context.Context, req llm.CompletionRequest, yield func(llm.CompletionChunk, error) bool) error {
	tok, err := p.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("openaicodex: fetch token: %w", err)
	}
	if tok.AccountID == "" {
		return ErrNoAccountID
	}

	// Resolve the model + reasoning effort from preset ids.
	model := p.model
	if m := optionString(req.Options, "model"); m != "" {
		model = m
	}
	baseModel, effort := resolveModel(model)
	// Effort precedence (highest first):
	//   1. req.Options["reasoning_effort"]      — per-request override
	//   2. preset's effort (from a "<base>-<effort>" id)
	//   3. provider.defaultEffort               — user setting
	//   4. defaultReasoningEffort(baseModel)    — model-name heuristic in buildRequest
	if effort == "" && p.defaultEffort != "" {
		effort = p.defaultEffort
	}
	if effort != "" {
		if req.Options == nil {
			req.Options = llm.ModelOptions{}
		}
		if _, set := req.Options["reasoning_effort"]; !set {
			req.Options["reasoning_effort"] = string(effort)
		}
	}

	// Pull system messages out of the history into the API's
	// `instructions` field. The Responses API treats `instructions` as
	// the system-prompt slot and would double-anchor the prompt if the
	// same content also appeared as a system-role message in `input`.
	//
	// This provider deliberately does NOT inject a Codex-CLI-style
	// system prompt. The caller (typically the zarlcode runner)
	// owns the system prompt — it already describes the actual tool
	// surface the model has, which differs from Codex CLI's. Adding a
	// canned Codex prompt on top would tell the model to call tools
	// (apply_patch, update_plan, etc.) we don't expose.
	instructions, body := splitSystemMessages(req)
	body.Messages = stripSystemMessages(body.Messages)
	reqBody := buildRequest(body, baseModel, instructions)

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("openaicodex: marshal request: %w", err)
	}

	resp, err := p.postResponses(ctx, payload, tok, optionString(req.Options, "prompt_cache_key"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Always parse as SSE: buildRequest forces stream=true for this
	// provider path and the Accept header asks the backend for
	// text/event-stream.
	return parseSSEStream(resp.Body, yield)
}

// postResponses issues the SSE-streaming POST to the Codex responses
// endpoint. Retry, exponential backoff, and Retry-After honouring are
// owned by the underlying [zhttp.Client] — see [newCodexClient].
//
// On success returns the open [*http.Response] with the body unread;
// the caller MUST close resp.Body. On any non-2xx status (after
// retries are exhausted for transient codes) returns a wrapped error
// carrying an 8 KiB excerpt of the response body for diagnostics,
// and never returns a live body. Stream-mid failures are not
// recoverable here — once SSE parsing begins the assistant turn is
// irrevocably committed.
func (p *Provider) postResponses(
	ctx context.Context,
	payload []byte,
	tok Token,
	cacheKey string,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+responsesPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("openaicodex: build http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+tok.Access)
	req.Header.Set("Chatgpt-Account-Id", tok.AccountID)
	req.Header.Set("Openai-Beta", "responses=experimental")
	req.Header.Set("Originator", originatorCodex)
	if cacheKey != "" {
		req.Header.Set("Session_id", cacheKey)
		req.Header.Set("Conversation_id", cacheKey)
	}

	resp, err := p.client.Do(ctx, req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("openaicodex: post responses: %w", err)
	}

	if resp.StatusCode/100 == 2 {
		return resp, nil
	}

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	_ = resp.Body.Close()
	msg := strings.TrimSpace(string(body))
	if readErr != nil {
		// A mid-read failure (connection reset) means msg is whatever
		// partial bytes arrived; note that so the diagnostic isn't read as
		// the provider's complete error body.
		return nil, fmt.Errorf("openaicodex: status %d (error body truncated: %w): %s", resp.StatusCode, readErr, msg)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, codexRateLimitError(msg, resp.Header)
	}
	return nil, fmt.Errorf("openaicodex: status %d: %s", resp.StatusCode, msg)
}

// splitSystemMessages returns (joinedSystemContent, req) where the
// returned req has the system messages still in place (so the caller
// can decide whether to drop them or pass them on as-is). The current
// caller drops them via stripSystemMessages.
func splitSystemMessages(req llm.CompletionRequest) (string, llm.CompletionRequest) {
	var b strings.Builder
	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem && m.Content != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(m.Content)
		}
	}
	return b.String(), req
}

// stripSystemMessages returns a copy of msgs without any system-role
// entries. The Responses API's `instructions` field is the canonical
// home for system content; leaving them in the input would
// double-anchor the prompt.
func stripSystemMessages(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}

// codexErrorBody is the Codex JSON error envelope. The rate-limit window
// rides in the body (resets_at unix seconds / resets_in_seconds) rather than
// in response headers, and message is a clean human string.
type codexErrorBody struct {
	Error struct {
		Type            string `json:"type"`
		Message         string `json:"message"`
		ResetsAt        int64  `json:"resets_at"`
		ResetsInSeconds int64  `json:"resets_in_seconds"`
	} `json:"error"`
}

// codexRateLimitError builds a *llm.RateLimitError from a Codex 429 response.
// It parses the JSON body for the human message and reset window so the raw
// JSON is never surfaced to the user, falling back to the standard headers
// when the body carries no timing.
func codexRateLimitError(body string, header http.Header) *llm.RateLimitError {
	rle := &llm.RateLimitError{Message: "codex: usage limit reached"}

	var parsed codexErrorBody
	if err := json.Unmarshal([]byte(body), &parsed); err == nil {
		if m := strings.TrimSpace(parsed.Error.Message); m != "" {
			rle.Message = m
		}
		if parsed.Error.ResetsAt > 0 {
			rle.ResetAt = time.Unix(parsed.Error.ResetsAt, 0)
		}
		if parsed.Error.ResetsInSeconds > 0 {
			rle.RetryAfter = time.Duration(parsed.Error.ResetsInSeconds) * time.Second
		}
		// A usage-limit verdict with no reset window is a hard stop; one
		// that resets is transient.
		rle.Permanent = parsed.Error.Type == "usage_limit_reached" &&
			rle.ResetAt.IsZero() && rle.RetryAfter == 0
	}

	if rle.RetryAfter == 0 {
		if ra := header.Get("Retry-After"); ra != "" {
			rle.RetryAfter = parseRetryAfter(ra)
		}
	}
	if rle.ResetAt.IsZero() {
		if rls := header.Get("X-Ratelimit-Reset"); rls != "" {
			rle.ResetAt = parseRateLimitReset(rls)
		}
	}
	return rle
}

func parseRetryAfter(v string) time.Duration {
	if d, err := strconv.Atoi(v); err == nil && d > 0 {
		return time.Duration(d) * time.Second
	}
	if t, err := time.Parse(time.RFC1123, v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func parseRateLimitReset(v string) time.Time {
	sec, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
