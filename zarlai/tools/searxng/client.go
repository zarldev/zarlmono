package searxng

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SearchResult is a single result from the SearXNG JSON API.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

// searchResponse mirrors the top-level SearXNG JSON response.
type searchResponse struct {
	Results []SearchResult `json:"results"`
}

// Client talks to the SearXNG JSON API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a Client targeting the given base URL with a 15-second timeout.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Search queries SearXNG and returns up to limit results.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	return c.searchCategory(ctx, query, "general", limit)
}

// SearchVideos queries SearXNG's videos category, returning video-oriented
// results (YouTube, Vimeo, etc.) up to limit.
func (c *Client) SearchVideos(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	return c.searchCategory(ctx, query, "videos", limit)
}

func (c *Client) searchCategory(ctx context.Context, query, category string, limit int) ([]SearchResult, error) {
	endpoint := c.baseURL + "/search"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "json")
	q.Set("categories", category)
	req.URL.RawQuery = q.Encode()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: unexpected status %d", resp.StatusCode)
	}

	var body searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := body.Results
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}
