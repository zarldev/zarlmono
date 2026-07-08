package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/zarldev/zarlmono/zkit/zhttp"
)

type wikiSummary struct {
	Title   string `json:"title"`
	Extract string `json:"extract"`
}

type quizQuestion struct {
	Prompt  string   `json:"prompt"`
	Answer  string   `json:"answer"`
	Choices []string `json:"choices"`
}

// https://en.wikipedia.org/api/rest_v1/).
//
// Wikipedia API policy requires:
//   - A descriptive User-Agent with contact information.
//   - No concurrent or bursty requests — serial with a gap is fine.
const wikiUserAgent = "zarlmono-computer-use-example/0.1 (https://github.com/zarldev/zarlmono)"

const wikiRandomURL = "https://en.wikipedia.org/api/rest_v1/page/random/summary"

func fetchSummaries(ctx context.Context, n int) ([]wikiSummary, error) {
	client := zhttp.NewClient(zhttp.WithUserAgent(wikiUserAgent))
	slog.InfoContext(ctx, "fetching random wikipedia summaries", "count", n)

	summaries := make([]wikiSummary, 0, n)
	seen := map[string]bool{}
	for attempts := 0; len(summaries) < n && attempts < n*15; attempts++ {
		if attempts > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}

		summary, err := fetchRandomSummary(ctx, client)
		if err != nil {
			slog.WarnContext(ctx, "wikipedia fetch failed, retrying", "attempt", attempts, "err", err)
			continue
		}
		if summary.Title == "" || len(summary.Extract) < 80 || seen[summary.Title] {
			continue
		}
		slog.DebugContext(ctx, "fetched summary", "title", summary.Title)
		summaries = append(summaries, summary)
	}
	if len(summaries) < n {
		return nil, fmt.Errorf("only fetched %d usable random summaries after %d attempts", len(summaries), n*15)
	}
	return summaries, nil
}

func fetchRandomSummary(ctx context.Context, client *zhttp.Client) (wikiSummary, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wikiRandomURL, nil)
	if err != nil {
		return wikiSummary{}, err
	}
	resp, err := client.Do(ctx, req)
	if err != nil {
		return wikiSummary{}, err
	}
	defer resp.Body.Close()
	var summary wikiSummary
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&summary); err != nil {
		return wikiSummary{}, err
	}
	return summary, nil
}
func rotate(values []string, n int) []string {
	out := append([]string(nil), values...)
	for range n {
		out = append(out[1:], out[0])
	}
	return out
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
