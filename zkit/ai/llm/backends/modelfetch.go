package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// modelsClient is the shared client for /models probes. zhttp's
// defaults match what we want: short whole-request timeout (model lists
// are small + fast), transport-level dial / TLS / header bounds, and
// retry with Retry-After honouring on 408 / 429 / 5xx.
var modelsClient = zhttp.NewClient()

// fetchOpenAICompatModels GETs /models on baseURL and decodes either
// shape commonly seen there:
//
//	OpenAI / DeepSeek / older llama.cpp: {"data": [{"id": ...}]}
//	Newer llama.cpp / ollama native:     {"models": [{"name": ...}]}
//
// Used by the no-auth local backends (llamacpp, ollama).
func fetchOpenAICompatModels(ctx context.Context, baseURL, _ string) ([]string, error) {
	return openaiCompatGet(ctx, baseURL, nil)
}

// fetchOpenAIBearerModels is fetchOpenAICompatModels with an
// Authorization: Bearer header — the OpenAI hosted API returns 401
// without it.
func fetchOpenAIBearerModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	if apiKey == "" {
		return nil, errors.New("API key not set")
	}
	return openaiCompatGet(ctx, baseURL, http.Header{
		"Authorization": []string{"Bearer " + apiKey},
	})
}

// fetchAnthropicModels hits Anthropic's /v1/models. Anthropic uses
// x-api-key + anthropic-version headers instead of bearer auth; the
// response shape matches OpenAI's ({data:[{id}]}).
func fetchAnthropicModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	if apiKey == "" {
		return nil, errors.New("ANTHROPIC_API_KEY not set")
	}
	url := baseURL
	if url == "" {
		url = "https://api.anthropic.com"
	}
	return openaiCompatGet(ctx, strings.TrimRight(url, "/")+"/v1", http.Header{
		"x-api-key":         []string{apiKey},
		"anthropic-version": []string{"2023-06-01"},
	})
}

// openaiCompatGet is the shared transport for /v1/models style
// endpoints. headers may be nil for no-auth servers.
func openaiCompatGet(ctx context.Context, baseURL string, headers http.Header) ([]string, error) {
	if baseURL == "" {
		return nil, errors.New("base URL not set")
	}
	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := modelsClient.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var oai struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &oai); err == nil && len(oai.Data) > 0 {
		ids := make([]string, 0, len(oai.Data))
		for _, m := range oai.Data {
			if m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}
	var native struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &native); err == nil && len(native.Models) > 0 {
		ids := make([]string, 0, len(native.Models))
		for _, m := range native.Models {
			id := m.Name
			if id == "" {
				id = m.Model
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	preview := string(body)
	if len(preview) > 200 {
		preview = preview[:200] + "…"
	}
	return nil, fmt.Errorf("response shape not recognised (expected {data:[]} or {models:[]}); got: %s", preview)
}
