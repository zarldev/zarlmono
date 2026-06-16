package llamacpp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// propsProbeTimeout bounds the /props request. It's a fast local call; if the
// server is slow or down we'd rather fall back to a default than hang startup.
const propsProbeTimeout = 3 * time.Second

// ProbeContextWindow asks a running llama.cpp server for the context window
// (n_ctx) it was launched with, by GETting {root}/props. baseURL may be the
// OpenAI-style endpoint (…/v1) or the bare root — the conventional /v1 suffix
// is stripped since /props lives at the server root. An empty baseURL falls
// back to [DefaultBaseURL].
//
// This is the server-configured window (the -c / --ctx-size the server was
// started with), which is the correct gauge denominator: the model may be
// trained for more, but the server only admits n_ctx tokens per request.
//
// Returns 0 when the server is unreachable, the response is unexpected, or
// n_ctx is absent — the caller keeps its existing default.
func ProbeContextWindow(ctx context.Context, baseURL string) int {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	// /props lives at the server root — strip the conventional /v1.
	root := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")

	ctx, cancel := context.WithTimeout(ctx, propsProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/props", nil)
	if err != nil {
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0
	}
	// /props returns deeply-nested JSON. The path on llama.cpp 5.x is
	// .default_generation_settings.n_ctx; older builds expose it at the top
	// level. Try both.
	var probe struct {
		NCtx                      int `json:"n_ctx"`
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return 0
	}
	if probe.NCtx > 0 {
		return probe.NCtx
	}
	return probe.DefaultGenerationSettings.NCtx
}
