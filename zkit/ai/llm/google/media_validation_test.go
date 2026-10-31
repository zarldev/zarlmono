package google_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cloud.google.com/go/auth"
	"google.golang.org/genai"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/google"
)

type googleRoute struct {
	name    string
	backend genai.Backend
}

var googleRoutes = []googleRoute{
	{name: "Gemini", backend: genai.BackendGeminiAPI},
	{name: "Vertex AI", backend: genai.BackendVertexAI},
}

func newGoogleRouteProvider(t *testing.T, route googleRoute, baseURL string) *google.Provider {
	t.Helper()
	cfg := &genai.ClientConfig{
		Backend:     route.backend,
		HTTPClient:  http.DefaultClient,
		HTTPOptions: genai.HTTPOptions{BaseURL: baseURL},
	}
	if route.backend == genai.BackendVertexAI {
		cfg.Project = "test-project"
		cfg.Location = "test-location"
		cfg.Credentials = &auth.Credentials{}
	} else {
		cfg.APIKey = "test-key"
	}
	client, err := genai.NewClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("new %s client: %v", route.name, err)
	}
	provider, err := google.NewProvider("", google.WithClient(client), google.WithModel("gemini-2.0-flash"))
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return provider
}

func TestProviderRejectsUnsupportedMediaBeforeDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		part llm.ContentPart
	}{
		{name: "URL image", role: llm.RoleUser, part: llm.ImagePartFromURL("https://example.invalid/image.png")},
		{name: "URL video", role: llm.RoleUser, part: llm.VideoPartFromURL("https://example.invalid/video.mp4")},
		{name: "malformed image data URI", role: llm.RoleUser, part: llm.ImagePartFromDataURI("data:image/png;base64,%%%", "image/png")},
		{name: "mismatched image MIME type", role: llm.RoleUser, part: llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/jpeg")},
		{name: "missing image payload", role: llm.RoleUser, part: llm.ContentPart{Type: llm.ContentTypeImage}},
		{name: "audio", role: llm.RoleUser, part: llm.ContentPart{Type: llm.ContentTypeAudio, Audio: &llm.AudioData{DataURI: "data:audio/wav;base64,d2F2", Format: "wav"}}},
		{name: "assistant image", role: llm.RoleAssistant, part: llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/png")},
		{name: "system video", role: llm.RoleSystem, part: llm.VideoPartFromDataURI("data:video/mp4;base64,bXA0", "video/mp4")},
		{name: "unknown", role: llm.RoleUser, part: llm.ContentPart{Type: llm.ContentPartType("document-canary-secret")}},
	}
	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
						requests.Add(1)
					}))
					t.Cleanup(server.Close)

					provider := newGoogleRouteProvider(t, route, server.URL)
					var streamErr error
					for _, err := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: []llm.Message{{
						Role:  test.role,
						Parts: []llm.ContentPart{llm.TextPart("inspect"), test.part},
					}}}) {
						streamErr = err
					}
					if !errors.Is(streamErr, llm.ErrModelNotSupported) {
						t.Fatalf("Complete error = %v, want ErrModelNotSupported", streamErr)
					}
					if strings.Contains(streamErr.Error(), "document-canary-secret") {
						t.Fatalf("Complete error exposed unknown input type: %v", streamErr)
					}
					if got := requests.Load(); got != 0 {
						t.Fatalf("HTTP requests = %d, want 0", got)
					}
				})
			}
		})
	}
}

type inlineMediaRequest struct {
	Contents []struct {
		Parts []struct {
			InlineData *struct {
				MIMEType string `json:"mimeType"`
				Data     string `json:"data"`
			} `json:"inlineData"`
		} `json:"parts"`
	} `json:"contents"`
}

func TestProviderPreservesSupportedInlineMedia(t *testing.T) {
	t.Parallel()

	for _, route := range googleRoutes {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			requests := make(chan inlineMediaRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request inlineMediaRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
				}
				requests <- request
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"))
			}))
			t.Cleanup(server.Close)

			provider := newGoogleRouteProvider(t, route, server.URL)
			messages := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{
				llm.TextPart("inspect"),
				llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/png"),
				llm.VideoPartFromDataURI("data:video/mp4;base64,bXA0", "video/mp4"),
			}}}
			for _, streamErr := range provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages}) {
				if streamErr != nil {
					t.Fatalf("Complete: %v", streamErr)
				}
			}

			request := <-requests
			if len(request.Contents) != 1 || len(request.Contents[0].Parts) != 3 {
				t.Fatalf("contents = %#v", request.Contents)
			}
			image := request.Contents[0].Parts[1].InlineData
			video := request.Contents[0].Parts[2].InlineData
			if image == nil || image.MIMEType != "image/png" || image.Data != "cG5n" {
				t.Fatalf("inline image = %#v", image)
			}
			if video == nil || video.MIMEType != "video/mp4" || video.Data != "bXA0" {
				t.Fatalf("inline video = %#v", video)
			}
		})
	}
}
