package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/service"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
	"golang.org/x/sync/errgroup"
)

// Conversation LLM settings — which backend the live chat path talks to.

func (a *AdminServer) GetConversationLLMSettings(ctx context.Context, req *connect.Request[zarlv1.GetConversationLLMSettingsRequest]) (*connect.Response[zarlv1.GetConversationLLMSettingsResponse], error) {
	provider, _ := a.settings.Get(ctx, "llm_provider")
	if provider == "" {
		provider = "ollama"
	}
	model, _ := a.settings.Get(ctx, "llm_model")
	baseURL, _ := a.settings.Get(ctx, "llm_base_url")
	apiKey, _ := a.settings.Get(ctx, "llm_api_key")
	reasoning, _ := a.settings.Get(ctx, "llm_reasoning")

	return connect.NewResponse(&zarlv1.GetConversationLLMSettingsResponse{
		Provider:         provider,
		Model:            model,
		BaseUrl:          baseURL,
		ApiKeyMasked:     maskKey(apiKey),
		ReasoningEnabled: reasoning == "true",
	}), nil
}

func (a *AdminServer) UpdateConversationLLMSettings(ctx context.Context, req *connect.Request[zarlv1.UpdateConversationLLMSettingsRequest]) (*connect.Response[zarlv1.UpdateConversationLLMSettingsResponse], error) {
	provider := req.Msg.Provider
	model := req.Msg.Model
	baseURL := req.Msg.BaseUrl
	apiKey := req.Msg.ApiKey
	reasoning := req.Msg.ReasoningEnabled

	if err := a.settings.Set(ctx, "llm_provider", provider); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist llm_provider: %w", err))
	}
	if err := a.settings.Set(ctx, "llm_model", model); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist llm_model: %w", err))
	}
	if err := a.settings.Set(ctx, "llm_base_url", baseURL); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist llm_base_url: %w", err))
	}
	if apiKey != "" {
		if err := a.settings.Set(ctx, "llm_api_key", apiKey); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist llm_api_key: %w", err))
		}
	}
	reasoningStr := "false"
	if reasoning {
		reasoningStr = "true"
	}
	if err := a.settings.Set(ctx, "llm_reasoning", reasoningStr); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist llm_reasoning: %w", err))
	}

	if apiKey == "" {
		apiKey, _ = a.settings.Get(ctx, "llm_api_key")
	}

	llm, err := a.buildLLM(LLMSpec{
		Provider:  provider,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Model:     model,
		Reasoning: reasoning,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	a.zarlServer.Reconfigure(WithLLM(llm))
	a.emitConfigChange(fmt.Sprintf("Conversation LLM changed to %s / %s", provider, model))

	return connect.NewResponse(&zarlv1.UpdateConversationLLMSettingsResponse{}), nil
}

// ListAvailableModels queries Ollama and llama-server concurrently and returns
// the merged model catalog. If either probe fails it is logged and skipped —
// a partial list is still useful.
func (a *AdminServer) ListAvailableModels(ctx context.Context, req *connect.Request[zarlv1.ListAvailableModelsRequest]) (*connect.Response[zarlv1.ListAvailableModelsResponse], error) {
	// Ollama: /api/tags endpoint, not the OpenAI-compatible /v1.
	// Operators set this via the admin UI; localhost:11434 is convention.
	ollamaBase, _ := a.settings.Get(ctx, "ollama_base_url")
	if ollamaBase == "" {
		ollamaBase = "http://localhost:11434"
	}
	// llama-server: the conversation LLM settings store this when
	// provider=llamacpp. Probed regardless of the current provider so
	// the operator can see what's available on both backends.
	llamaBase, _ := a.settings.Get(ctx, "llm_base_url")
	if llamaBase == "" {
		llamaBase = "http://localhost:8081"
	}

	var (
		ollamaModels []*zarlv1.AvailableModelMsg
		llamaModels  []*zarlv1.AvailableModelMsg
	)
	var g errgroup.Group
	g.Go(func() error { ollamaModels = probeOllamaModels(ollamaBase); return nil })
	g.Go(func() error { llamaModels = probeLlamacppModels(llamaBase); return nil })
	// errgroup never returns a non-nil error here (every probe logs and
	// swallows), but we still call Wait to synchronise the goroutines.
	_ = g.Wait()

	all := make([]*zarlv1.AvailableModelMsg, 0, len(ollamaModels)+len(llamaModels))
	all = append(all, ollamaModels...)
	all = append(all, llamaModels...)
	sort.Slice(all, func(i, j int) bool {
		if all[i].Provider != all[j].Provider {
			return all[i].Provider < all[j].Provider
		}
		return all[i].Name < all[j].Name
	})
	return connect.NewResponse(&zarlv1.ListAvailableModelsResponse{Models: all}), nil
}

// probeOllamaModels asks an Ollama instance for its model catalog via
// /api/tags. Failures (unreachable server, bad JSON) log a warning and
// return nil so ListAvailableModels still returns whatever the other
// probe found.
func probeOllamaModels(baseURL string) []*zarlv1.AvailableModelMsg {
	url := baseURL + "/api/tags"
	resp, err := http.Get(url) //nolint:gosec // URL is operator-supplied
	if err != nil {
		slog.Warn("ollama probe failed", "url", url, "err", err)
		return nil
	}
	defer resp.Body.Close()

	var payload struct {
		Models []struct {
			Name    string `json:"name"`
			Details struct {
				ParameterSize string `json:"parameter_size"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		slog.Warn("ollama response decode failed", "err", err)
		return nil
	}
	out := make([]*zarlv1.AvailableModelMsg, 0, len(payload.Models))
	for _, m := range payload.Models {
		out = append(out, &zarlv1.AvailableModelMsg{
			Name:     m.Name,
			Provider: "ollama",
			Size:     m.Details.ParameterSize,
		})
	}
	return out
}

// probeLlamacppModels asks a llama-server instance for its model
// catalog via /v1/models. Failures log + return nil. Strips trailing
// /v1 (and stray /) so a baseURL stored as `http://host:8081/v1` —
// the form the chat client expects — doesn't produce /v1/v1/models.
func probeLlamacppModels(baseURL string) []*zarlv1.AvailableModelMsg {
	base := strings.TrimRight(baseURL, "/")
	base = strings.TrimSuffix(base, "/v1")
	url := base + "/v1/models"
	resp, err := http.Get(url) //nolint:gosec // URL is operator-supplied
	if err != nil {
		slog.Warn("llamacpp probe failed", "url", url, "err", err)
		return nil
	}
	defer resp.Body.Close()

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		slog.Warn("llamacpp response decode failed", "err", err)
		return nil
	}
	out := make([]*zarlv1.AvailableModelMsg, 0, len(payload.Data))
	for _, m := range payload.Data {
		out = append(out, &zarlv1.AvailableModelMsg{
			Name:     m.ID,
			Provider: "llamacpp",
		})
	}
	return out
}

// SetConversationModel writes only the llm_model setting and reconfigures
// the live LLM with the existing provider/baseURL/apiKey/reasoning. Used
// by the onboarding wizard's model picker; full LLM config still lives
// in UpdateConversationLLMSettings.
func (a *AdminServer) SetConversationModel(ctx context.Context, req *connect.Request[zarlv1.SetConversationModelRequest]) (*connect.Response[zarlv1.SetConversationModelResponse], error) {
	model := strings.TrimSpace(req.Msg.Model)
	if model == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("model is required"))
	}
	if err := a.settings.Set(ctx, "llm_model", model); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("persist llm_model: %w", err))
	}
	provider, _ := a.settings.Get(ctx, "llm_provider")
	if provider == "" {
		provider = "ollama"
	}
	baseURL, _ := a.settings.Get(ctx, "llm_base_url")
	apiKey, _ := a.settings.Get(ctx, "llm_api_key")
	reasoning, _ := a.settings.Get(ctx, "llm_reasoning")
	if a.zarlServer != nil {
		llm, err := a.buildLLM(LLMSpec{
			Provider:  provider,
			BaseURL:   baseURL,
			APIKey:    apiKey,
			Model:     model,
			Reasoning: reasoning == "true",
		})
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		a.zarlServer.Reconfigure(WithLLM(llm))
	}
	a.emitConfigChange(fmt.Sprintf("Conversation LLM model changed to %s", model))
	return connect.NewResponse(&zarlv1.SetConversationModelResponse{}), nil
}

// LLMSpec captures the configuration the admin UI persists for the
// live conversation LLM. Bundled into a struct so adding fields (e.g.
// per-model max tokens, request timeout) doesn't break the three call
// sites that build one.
type LLMSpec struct {
	Provider  string
	BaseURL   string
	APIKey    string
	Model     string
	Reasoning bool
}

func (a *AdminServer) buildLLM(spec LLMSpec) (service.LLM, error) {
	switch spec.Provider {
	case "ollama":
		return service.NewOllamaClient(spec.BaseURL, spec.Model, a.embedder, service.WithOllamaReasoning(spec.Reasoning)), nil
	case "llamacpp":
		return service.NewLlamaCppClient(spec.BaseURL, spec.Model, a.embedder, service.WithLlamaCppReasoning(spec.Reasoning)), nil
	case "openai":
		if spec.APIKey == "" {
			return nil, fmt.Errorf("openai provider requires API key")
		}
		// OpenAI doesn't implement Embed — wrap with the existing embedder.
		// Reasoning is ignored: the public OpenAI API doesn't expose it.
		return service.NewLlamaCppClient(spec.BaseURL, spec.Model, a.embedder), nil
	default:
		return nil, fmt.Errorf("unknown conversation provider %q", spec.Provider)
	}
}
