package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAIEmbedder is a provider-agnostic embedding client. It speaks the
// OpenAI /v1/embeddings shape (`{model, input}` request, `{data: [{embedding: [...]}]}`
// response) so any compatible endpoint works: Ollama (point at
// http://localhost:11434/v1), OpenAI proper, OpenRouter, vLLM,
// llama.cpp's `--embeddings` mode, etc.
type OpenAIEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

var _ Embedder = (*OpenAIEmbedder)(nil)

// NewOpenAIEmbedder constructs an embedder pointed at any
// OpenAI-compatible /v1/embeddings endpoint. baseURL should NOT include
// the trailing /embeddings — it's the API root (e.g.
// "http://localhost:11434/v1" for Ollama, "https://api.openai.com/v1"
// for OpenAI proper). Trailing slashes are tolerated.
func NewOpenAIEmbedder(baseURL, model string) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

type openAIEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed returns a single vector for the input text.
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(openAIEmbedRequest{Model: e.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send embed request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: unexpected status %d", resp.StatusCode)
	}

	var out openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(out.Data) == 0 || len(out.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embed: empty vector in response")
	}
	return out.Data[0].Embedding, nil
}
