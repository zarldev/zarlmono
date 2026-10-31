package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var searchHealthClient = &http.Client{Timeout: 10 * time.Second}

// CheckSearch checks that endpoint serves SearXNG's JSON search contract. An
// empty results array is healthy; callers must assess search relevance separately.
// Responses are bounded to 1 MiB and each request is limited to ten seconds or
// the caller's earlier deadline. It does not install or start a service.
func CheckSearch(ctx context.Context, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse search endpoint: %w", err)
	}
	u.Path = "/search"
	u.RawQuery = url.Values{"q": {"zarlcode"}, "format": {"json"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("build search health request: %w", err)
	}
	resp, err := searchHealthClient.Do(req)
	if err != nil {
		return fmt.Errorf("check search endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden {
			return errors.New("SearXNG returned HTTP 403; enable json in search.formats")
		}
		return fmt.Errorf("SearXNG returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("read search health response: %w", err)
	}
	if len(body) > maxBody {
		return errors.New("search health response exceeds 1 MiB")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode search health response: %w", err)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(payload["results"]), []byte("[")) {
		return errors.New("search health response has no results array")
	}
	return nil
}

func waitForSearch(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := CheckSearch(ctx, SearxngURL)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for SearXNG readiness: %w", errors.Join(err, context.Cause(ctx)))
		case <-ticker.C:
		}
	}
}
