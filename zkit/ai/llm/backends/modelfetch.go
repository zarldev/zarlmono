package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

// modelsClient is the shared client for unauthenticated and bearer-authenticated
// /models probes. zhttp's defaults match what we want: short whole-request
// timeout, bounded transport operations, and transient-response retries.
var modelsClient = zhttp.NewClient()

// googleModelsClient is separate because Google discovery carries an API key in
// a custom header, which net/http may preserve across cross-origin redirects.
// Rejecting every redirect keeps that credential bound to the configured origin.
var googleModelsClient = newGoogleModelsClient()

func newGoogleModelsClient() *zhttp.Client {
	client := zhttp.NewClient()
	client.HTTPClient().CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

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
const (
	modelListPageLimit = "1000"
	maxModelListPages  = 20
)

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
	return openaiCompatGetCursor(ctx, strings.TrimRight(url, "/")+"/v1", http.Header{
		"x-api-key":         []string{apiKey},
		"anthropic-version": []string{"2023-06-01"},
	}, "after_id", true)
}

func fetchGoogleModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	if apiKey == "" {
		return nil, errors.New("google API key not set")
	}
	endpoint := googleModelsEndpoint(baseURL)
	ids := make([]string, 0)
	seen := make(map[string]bool)
	pageToken := ""
	for range maxModelListPages {
		reqURL, err := modelListURL(endpoint, "pageToken", pageToken, false)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build Google models request: %w", err)
		}
		req.Header.Set("X-Goog-Api-Key", apiKey)
		resp, err := googleModelsClient.Do(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("GET Google models: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read Google models body: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close Google models body: %w", closeErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("google models status %d", resp.StatusCode)
		}
		var result struct {
			Models []struct {
				Name                       string   `json:"name"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
				SupportedActions           []string `json:"supportedActions"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decode Google models: %w", err)
		}
		for _, model := range result.Models {
			if !slices.Contains(model.SupportedGenerationMethods, "generateContent") && !slices.Contains(model.SupportedActions, "generateContent") {
				continue
			}
			id := strings.TrimPrefix(model.Name, "models/")
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if result.NextPageToken == "" || result.NextPageToken == pageToken {
			return ids, nil
		}
		pageToken = result.NextPageToken
	}
	return ids, nil
}

func googleModelsEndpoint(baseURL string) string {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	// The pinned GenAI SDK treats BaseURL as an unversioned prefix and appends
	// its Gemini API default version before the operation path.
	return strings.TrimRight(baseURL, "/") + "/v1beta/models"
}

// openaiCompatGet is the shared transport for /v1/models style
// endpoints. headers may be nil for no-auth servers.
func openaiCompatGet(ctx context.Context, baseURL string, headers http.Header) ([]string, error) {
	return openaiCompatGetCursor(ctx, baseURL, headers, "after", false)
}

func openaiCompatGetCursor(ctx context.Context, baseURL string, headers http.Header, cursorParam string, includeLimit bool) ([]string, error) {
	if baseURL == "" {
		return nil, errors.New("base URL not set")
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/models"
	ids := make([]string, 0)
	seen := make(map[string]bool)
	cursor := ""
	for range maxModelListPages {
		reqURL, err := modelListURL(endpoint, cursorParam, cursor, includeLimit)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
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
			return nil, fmt.Errorf("GET %s: %w", reqURL, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read body: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close body: %w", closeErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			preview := string(body)
			if len(preview) > 512 {
				preview = preview[:512]
			}
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, preview)
		}
		var oai struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(body, &oai); err == nil && len(oai.Data) > 0 {
			last := ""
			for _, m := range oai.Data {
				last = m.ID
				if m.ID != "" && !seen[m.ID] {
					seen[m.ID] = true
					ids = append(ids, m.ID)
				}
			}
			if !oai.HasMore || cursorParam == "" {
				return ids, nil
			}
			next := oai.LastID
			if next == "" {
				next = last
			}
			if next == "" || next == cursor {
				return ids, nil
			}
			cursor = next
			continue
		}
		var native struct {
			Models []struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &native); err == nil && len(native.Models) > 0 {
			for _, m := range native.Models {
				id := m.Name
				if id == "" {
					id = m.Model
				}
				if id != "" && !seen[id] {
					seen[id] = true
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
	return ids, nil
}

func modelListURL(endpoint, cursorParam, cursor string, includeLimit bool) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse models URL: %w", err)
	}
	q := u.Query()
	if includeLimit {
		q.Set("limit", modelListPageLimit)
	}
	if cursorParam != "" && cursor != "" {
		q.Set(cursorParam, cursor)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
