package anthropic_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/anthropic"
)

func TestProviderRejectsUnsupportedMediaBeforeDispatch(t *testing.T) {
	t.Parallel()
	image := llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/png")
	cases := []struct {
		name string
		role string
		part llm.ContentPart
	}{
		{name: "missing payload", part: llm.ContentPart{Type: llm.ContentTypeImage}},
		{name: "empty payload", part: llm.ContentPart{Type: llm.ContentTypeImage, Image: &llm.ImageData{}}},
		{name: "non data URI", part: llm.ImagePartFromDataURI("image/png;base64,cG5n", "image/png")},
		{name: "non base64 URI", part: llm.ImagePartFromDataURI("data:image/png,cG5n", "image/png")},
		{name: "empty base64", part: llm.ImagePartFromDataURI("data:image/png;base64,", "image/png")},
		{name: "malformed base64", part: llm.ImagePartFromDataURI("data:image/png;base64,%%%", "image/png")},
		{name: "truncated base64", part: llm.ImagePartFromDataURI("data:image/png;base64,cG5", "image/png")},
		{name: "missing media type", part: llm.ImagePartFromDataURI("data:;base64,cG5n", "image/png")},
		{name: "unsupported image type", part: llm.ImagePartFromDataURI("data:image/svg+xml;base64,cG5n", "image/svg+xml")},
		{name: "non image type", part: llm.ImagePartFromDataURI("data:application/pdf;base64,cG5n", "application/pdf")},
		{name: "contradictory media type", part: llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/jpeg")},
		{name: "relative URL", part: llm.ImagePartFromURL("/screenshot.png")},
		{name: "unsupported URL scheme", part: llm.ImagePartFromURL("file:///screenshot.png")},
		{name: "URL without host", part: llm.ImagePartFromURL("https:///screenshot.png")},
		{name: "malformed URL", part: llm.ImagePartFromURL("https://example.com/%zz")},
		{name: "no fallback from malformed data URI", part: llm.ContentPart{Type: llm.ContentTypeImage, Image: &llm.ImageData{DataURI: "invalid", URL: "https://example.com/screenshot.png"}}},
		{name: "tool missing payload", role: llm.RoleTool, part: llm.ContentPart{Type: llm.ContentTypeImage}},
		{name: "tool malformed data URI", role: llm.RoleTool, part: llm.ImagePartFromDataURI("invalid", "image/png")},
		{name: "assistant image", role: llm.RoleAssistant, part: image},
		{name: "system image", role: llm.RoleSystem, part: image},
		{name: "unknown role image", role: "unknown", part: image},
		{name: "audio", part: llm.ContentPart{Type: llm.ContentTypeAudio}},
		{name: "video", part: llm.ContentPart{Type: llm.ContentTypeVideo}},
		{name: "unknown part", part: llm.ContentPart{Type: "unknown"}},
	}
	for _, stream := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.name+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
				t.Parallel()
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{}}`))
				}))
				t.Cleanup(server.Close)
				role := tc.role
				if role == "" {
					role = llm.RoleUser
				}
				messages := []llm.Message{{Role: role, Content: "keep this text", Parts: []llm.ContentPart{tc.part}}}
				before := llm.CloneMessages(messages)
				provider := anthropic.NewProvider("synthetic-key", anthropic.WithBaseURL(server.URL))
				completion := provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages, Stream: stream})
				if requests.Load() != 0 {
					t.Fatal("Complete dispatched before iteration")
				}
				yields := 0
				for chunk, err := range completion {
					yields++
					if !errors.Is(err, llm.ErrModelNotSupported) {
						t.Fatalf("Complete error = %v, want ErrModelNotSupported", err)
					}
					if !reflect.DeepEqual(chunk, llm.CompletionChunk{}) {
						t.Errorf("terminal chunk = %#v, want empty", chunk)
					}
				}
				if yields != 1 || requests.Load() != 0 {
					t.Errorf("yields = %d, HTTP requests = %d, want 1 and 0", yields, requests.Load())
				}
				if !reflect.DeepEqual(messages, before) {
					t.Error("media validation mutated input messages")
				}
			})
		}
	}
}

func TestProviderPreservesUserAndToolImages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		image  llm.ContentPart
		source map[string]any
	}{
		{name: "https URL", image: llm.ImagePartFromURL("https://example.com/screenshot.png"), source: map[string]any{"type": "url", "url": "https://example.com/screenshot.png"}},
		{name: "http URL", image: llm.ImagePartFromURL("http://example.com/screenshot.png"), source: map[string]any{"type": "url", "url": "http://example.com/screenshot.png"}},
		{name: "data URI preferred", image: llm.ContentPart{Type: llm.ContentTypeImage, Image: &llm.ImageData{URL: "https://example.com/unused.png", DataURI: "data:image/png;base64,cG5n"}}, source: map[string]any{"type": "base64", "media_type": "image/png", "data": "cG5n"}},
	}
	for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		cases = append(cases, struct {
			name   string
			image  llm.ContentPart
			source map[string]any
		}{name: mime, image: llm.ImagePartFromDataURI("data:"+mime+";base64,cG5n", mime), source: map[string]any{"type": "base64", "media_type": mime, "data": "cG5n"}})
	}
	for _, stream := range []bool{false, true} {
		for _, role := range []string{llm.RoleUser, llm.RoleTool} {
			for _, tc := range cases {
				t.Run(tc.name+"/"+role+"/stream="+strconv.FormatBool(stream), func(t *testing.T) {
					t.Parallel()
					messages := []llm.Message{{Role: role, Content: "tool output", ToolCallID: "screen-1", Parts: []llm.ContentPart{llm.TextPart("describe this image"), tc.image}}}
					if role == llm.RoleTool {
						messages = append([]llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "screen-1", Function: llm.ToolCallFunction{Name: "computer_observe", Arguments: `{}`}}}}}, messages...)
					}
					before := llm.CloneMessages(messages)
					body := captureModernAnthropic(t, "claude-sonnet-4-6", llm.CompletionRequest{Messages: messages, Stream: stream})
					wireMessages := body["messages"].([]any)
					content := messageContent(t, wireMessages[len(wireMessages)-1])
					if role == llm.RoleTool {
						if len(content) != 4 || content[0]["type"] != "tool_result" || content[0]["tool_use_id"] != "screen-1" || !strings.Contains(content[1]["text"].(string), "screen-1") {
							t.Fatalf("tool attachment attribution = %#v", content)
						}
						result := messageContent(t, content[0])
						if len(result) != 1 || result[0]["text"] != "tool output" {
							t.Fatalf("tool result content = %#v", result)
						}
						content = content[2:]
					}
					if len(content) != 2 || content[0]["type"] != "text" || content[0]["text"] != "describe this image" || content[1]["type"] != "image" {
						t.Fatalf("image content = %#v", content)
					}
					if !reflect.DeepEqual(content[1]["source"], tc.source) {
						t.Errorf("image source = %#v, want %#v", content[1]["source"], tc.source)
					}
					if !reflect.DeepEqual(messages, before) {
						t.Error("image serialization mutated input messages")
					}
				})
			}
		}
	}
}

func TestProviderMediaValidationBeginsOnIteration(t *testing.T) {
	t.Parallel()
	for _, stream := range []bool{false, true} {
		t.Run(strconv.FormatBool(stream), func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)
			provider := anthropic.NewProvider("synthetic-key", anthropic.WithBaseURL(server.URL))
			messages := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{llm.ImagePartFromDataURI("data:image/png;base64,cG5n", "image/png")}}}
			completion := provider.Complete(t.Context(), llm.CompletionRequest{Messages: messages, Stream: stream})
			// Mutate before iteration (not during borrowing) to prove validation
			// reads the external representation only when the stream is invoked.
			messages[0].Parts[0].Image.DataURI = "malformed"
			yields := 0
			for _, err := range completion {
				yields++
				if !errors.Is(err, llm.ErrModelNotSupported) {
					t.Fatalf("Complete error = %v, want ErrModelNotSupported", err)
				}
			}
			if requests.Load() != 0 {
				t.Errorf("HTTP requests = %d, want no dispatch", requests.Load())
			}
			if yields != 1 {
				t.Errorf("yields = %d, want one validation error", yields)
			}
		})
	}
}
