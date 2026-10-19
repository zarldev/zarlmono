package openaicodex_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestGenerateImageUsesNativeCodexToolAndReturnsImage(t *testing.T) {
	pngData := testPNG(t)
	encoded := base64.StdEncoding.EncodeToString(pngData)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s, want POST /codex/responses", r.Method, r.URL.Path)
		}
		if r.Header.Get("Chatgpt-Account-Id") != "acct-image" || r.Header.Get("Authorization") != "Bearer token-image" {
			t.Error("Codex OAuth headers were not sent")
		}
		var body struct {
			Model string `json:"model"`
			Input []struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
			Tools []struct {
				Type   string `json:"type"`
				Model  string `json:"model"`
				Action string `json:"action"`
			} `json:"tools"`
			ToolChoice string `json:"tool_choice"`
			Stream     bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if body.Model != "gpt-6-astra" || body.ToolChoice != "required" || !body.Stream || len(body.Tools) != 1 ||
			body.Tools[0].Type != "image_generation" || body.Tools[0].Model != "gpt-image-2" || body.Tools[0].Action != "generate" {
			t.Errorf("native image-generation request = %+v", body)
		}
		if len(body.Input) != 1 || len(body.Input[0].Content) != 1 || body.Input[0].Content[0].Text != "A tiny test image" {
			t.Errorf("input = %+v", body.Input)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.output_item.done","output_index":0,"item":{"type":"image_generation_call","result":"`+encoded+`","revised_prompt":"tiny test prompt"}}`)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	provider := openaicodex.NewProvider(
		openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "token-image", AccountID: "acct-image"}},
		openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry(), openaicodex.WithModel("gpt-6-astra"),
	)
	if _, ok := any(provider).(llm.ImageGenerator); !ok {
		t.Fatal("Codex provider does not expose image generation")
	}
	got, err := provider.GenerateImage(t.Context(), llm.ImageGenerationRequest{Prompt: "A tiny test image"})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if !bytes.Equal(got.Data, pngData) || got.MIMEType != "image/png" || got.RevisedPrompt != "tiny test prompt" {
		t.Fatalf("GeneratedImage = %+v", got)
	}
}

func TestGenerateImageIncludesReferenceImage(t *testing.T) {
	pngData := testPNG(t)
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		input := body["input"].([]any)
		content := input[0].(map[string]any)["content"].([]any)
		if len(content) != 2 || content[0].(map[string]any)["text"] != "Edit this" || content[1].(map[string]any)["image_url"] != dataURI {
			t.Errorf("reference image input = %#v", content)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(w, `data: {"type":"response.output_item.done","item":{"type":"image_generation_call","result":"`+base64.StdEncoding.EncodeToString(pngData)+`"}}`)
	}))
	t.Cleanup(server.Close)
	provider := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "token", AccountID: "acct"}}, openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry())
	_, err := provider.GenerateImage(t.Context(), llm.ImageGenerationRequest{
		Prompt: "Edit this", ReferenceImages: []llm.ContentPart{llm.ImagePartFromDataURI(dataURI, "image/png")},
	})
	if err != nil {
		t.Fatalf("GenerateImage with reference: %v", err)
	}
}

func TestGenerateImageRejectsInvalidAndOversizedResponses(t *testing.T) {
	for _, result := range []string{"not-base64!", base64.StdEncoding.EncodeToString([]byte("not png"))} {
		t.Run(result, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"type":"response.output_item.done","item":{"type":"image_generation_call","result":"`+result+`"}}`)
			}))
			t.Cleanup(server.Close)
			provider := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "token", AccountID: "acct"}}, openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry())
			if _, err := provider.GenerateImage(t.Context(), llm.ImageGenerationRequest{Prompt: "valid prompt"}); err == nil {
				t.Fatal("invalid generated image was accepted")
			}
		})
	}
}

func TestGenerateImageHonorsCancellation(t *testing.T) {
	entered := make(chan struct{})
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-serverDone:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(serverDone) })
	provider := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "token", AccountID: "acct"}}, openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry())
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := provider.GenerateImage(ctx, llm.ImageGenerationRequest{Prompt: "valid prompt"})
		result <- err
	}()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("GenerateImage error = %v, want context.Canceled", err)
	}
}

func TestGenerateImageRejectsInvalidPromptBeforeRequest(t *testing.T) {
	provider := openaicodex.NewProvider(openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "token", AccountID: "acct"}})
	for _, prompt := range []string{"", strings.Repeat("x", 64*1024+1)} {
		if _, err := provider.GenerateImage(t.Context(), llm.ImageGenerationRequest{Prompt: prompt}); err == nil {
			t.Fatal("invalid prompt was accepted")
		}
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
