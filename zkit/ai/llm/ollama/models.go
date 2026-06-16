package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// ContextWindowFor asks an Ollama server what context window the
// named model was loaded with. Ollama doesn't ship a static
// per-model table the way OpenAI / Anthropic do — the window is
// whatever `num_ctx` the modelfile (and any user override) pinned
// at load time, so the only reliable answer is to ask the server.
//
// Wire format: POST /api/show with {"name": "<model>"} returns a
// JSON blob whose model_info map contains "<arch>.context_length"
// (eg "llama.context_length", "qwen2.context_length"). Older
// servers expose the same value under details.parameter_size /
// .num_ctx; both shapes are tolerated.
//
// Returns 0 on any error so the caller's fallback chain
// (LLM_CONTEXT env, conservative default) stays in charge. baseURL
// follows the same convention as the rest of the OpenAI-compat
// surface: strip a trailing /v1 because /api/show lives at the
// server root.
func ContextWindowFor(ctx context.Context, baseURL, model string) int {
	if model == "" || baseURL == "" {
		return 0
	}
	root := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	payload, err := json.Marshal(map[string]string{"name": model})
	if err != nil {
		return 0
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, root+"/api/show", bytes.NewReader(payload))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return 0
	}
	return parseShowResponse(body)
}

// parseShowResponse extracts the context window from an /api/show
// JSON body. Exposed only for tests; the production path is
// ContextWindowFor.
func parseShowResponse(body []byte) int {
	// Decode into a permissive shape — model_info is a map keyed by
	// "<arch>.context_length" (architecture-prefixed), so we look
	// for any key matching that suffix rather than enumerating
	// every known arch. The details.num_ctx fallback handles older
	// servers that don't expose model_info.
	var probe struct {
		ModelInfo map[string]json.RawMessage `json:"model_info"`
		Details   struct {
			NumCtx        int    `json:"num_ctx"`
			ParameterSize string `json:"parameter_size"`
		} `json:"details"`
		// Some Ollama builds surface n_ctx at the top level when the
		// modelfile pinned PARAMETER num_ctx — accept it too.
		NumCtx int `json:"num_ctx"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return 0
	}
	for key, raw := range probe.ModelInfo {
		if !strings.HasSuffix(key, ".context_length") {
			continue
		}
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			continue
		}
		if n > 0 {
			return n
		}
	}
	if probe.NumCtx > 0 {
		return probe.NumCtx
	}
	if probe.Details.NumCtx > 0 {
		return probe.Details.NumCtx
	}
	return 0
}
