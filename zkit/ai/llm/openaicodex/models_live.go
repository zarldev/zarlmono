package openaicodex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/zhttp"
)

const modelsPath = "/codex/models"

var codexModelsClient = zhttp.NewClient()

type codexModelsResponse struct {
	Models []codexModelInfo `json:"models"`
}

type codexModelInfo struct {
	Slug                          string `json:"slug"`
	DisplayName                   string `json:"display_name"`
	Description                   string `json:"description"`
	ContextWindow                 int    `json:"context_window"`
	MaxContextWindow              int    `json:"max_context_window"`
	AutoCompactTokenLimit         int    `json:"auto_compact_token_limit"`
	EffectiveContextWindowPercent int    `json:"effective_context_window_percent"`
}

// FetchModels asks the ChatGPT-account Codex backend for its live model
// catalogue. The backend advertises auto_compact_token_limit, context_window,
// and effective_context_window_percent here; zarlcode should prefer those
// values over public/API docs because this OAuth path can have a smaller usable
// cap and its own compaction threshold.
func FetchModels(ctx context.Context, tokens TokenSource, baseURL string) ([]llm.Model, error) {
	if tokens == nil {
		return nil, errors.New("openaicodex: fetch models: TokenSource is required")
	}
	tok, err := tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("openaicodex: fetch models token: %w", err)
	}
	if tok.AccountID == "" {
		return nil, ErrNoAccountID
	}
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = defaultBaseURL
	}
	u, err := url.Parse(base + modelsPath)
	if err != nil {
		return nil, fmt.Errorf("openaicodex: build models url: %w", err)
	}
	q := u.Query()
	q.Set("client_version", "zarlcode")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("openaicodex: build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok.Access)
	req.Header.Set("Chatgpt-Account-Id", tok.AccountID)
	req.Header.Set("Originator", originatorCodex)

	resp, err := codexModelsClient.Do(ctx, req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("openaicodex: get models: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("openaicodex: read models: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("openaicodex: models status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var wire codexModelsResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("openaicodex: decode models: %w", err)
	}
	models := make([]llm.Model, 0, len(wire.Models))
	for _, m := range wire.Models {
		if m.Slug == "" {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = m.Slug
		}
		models = append(models, llm.Model{
			ID:          m.Slug,
			Name:        name,
			Description: m.Description,
			MaxTokens:   usableContextWindow(m),
			InputCost:   0,
			OutputCost:  0,
			Capabilities: llm.ModelCapabilities{
				SupportsStreaming: true,
				SupportsTools:     true,
				SupportsSystem:    true,
				SupportsThinking:  true,
				SupportsVision:    true,
			},
		})
	}
	return models, nil
}

// FetchContextWindow returns the backend-advertised compaction budget for model.
// Prefer auto_compact_token_limit when present because it is the backend's
// explicit pressure threshold; otherwise derive the same default upstream Codex
// uses for automatic compaction: 90% of the advertised context_window.
func FetchContextWindow(ctx context.Context, tokens TokenSource, baseURL, model string) (int, error) {
	models, err := FetchModels(ctx, tokens, baseURL)
	if err != nil {
		return 0, err
	}
	baseModel, _ := resolveModel(model)
	if baseModel == "" {
		baseModel = defaultModel
	}
	for _, m := range models {
		if m.ID == baseModel || m.ID == model {
			return m.MaxTokens, nil
		}
	}
	return 0, fmt.Errorf("openaicodex: model %q not found in backend catalogue", model)
}

func usableContextWindow(m codexModelInfo) int {
	ctx := m.ContextWindow
	if ctx <= 0 {
		ctx = m.MaxContextWindow
	}
	if ctx > 0 {
		contextLimit := ctx * 9 / 10
		if m.AutoCompactTokenLimit > 0 && m.AutoCompactTokenLimit < contextLimit {
			return m.AutoCompactTokenLimit
		}
		return contextLimit
	}
	if m.AutoCompactTokenLimit > 0 {
		return m.AutoCompactTokenLimit
	}
	return DefaultContextWindow
}
