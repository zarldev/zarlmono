package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ContextWindowFor asks an Ollama server what context window the
// named model was loaded with. Ollama doesn't ship a static
// per-model table the way OpenAI / Anthropic do — the window is
// whatever `num_ctx` the modelfile (and any user override) pinned
// at load time, so the only reliable answer is to ask the server.
//
// GET /api/ps reports loaded models' effective context_length. If the model
// is not loaded, POST /api/show can supply an explicit num_ctx configuration.
// Architectural model_info capacity alone is not a runtime context guarantee.
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
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if body := contextProbe(probeCtx, http.MethodGet, root+"/api/ps", nil); body != nil {
		var loaded struct {
			Models []struct {
				Name          string `json:"name"`
				Model         string `json:"model"`
				ContextLength int    `json:"context_length"`
			} `json:"models"`
		}
		if json.Unmarshal(body, &loaded) == nil {
			for _, running := range loaded.Models {
				if (sameOllamaModel(running.Name, model) || sameOllamaModel(running.Model, model)) && running.ContextLength > 0 {
					return running.ContextLength
				}
			}
		}
	}
	payload, err := json.Marshal(map[string]string{"name": model})
	if err != nil {
		return 0
	}
	return parseShowResponse(contextProbe(probeCtx, http.MethodPost, root+"/api/show", bytes.NewReader(payload)))
}

func sameOllamaModel(left, right string) bool {
	return strings.TrimSuffix(left, ":latest") == strings.TrimSuffix(right, ":latest")
}

func contextProbe(ctx context.Context, method, endpoint string, body io.Reader) []byte {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if err != nil {
		return nil
	}
	return data
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
		Parameters string `json:"parameters"`
		Details    struct {
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
	if probe.NumCtx > 0 {
		return probe.NumCtx
	}
	if probe.Details.NumCtx > 0 {
		return probe.Details.NumCtx
	}
	for _, line := range strings.Split(probe.Parameters, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "num_ctx" {
			if value, err := strconv.Atoi(fields[1]); err == nil && value > 0 {
				return value
			}
		}
	}
	return 0
}
