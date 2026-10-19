package openaicodex_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openaicodex"
)

func TestGenerateImageReturnsAtTerminalEventBeforeEOF(t *testing.T) {
	pngData := testPNG(t)
	encoded := base64.StdEncoding.EncodeToString(pngData)
	for _, terminal := range []string{"completed", "done"} {
		t.Run(terminal, func(t *testing.T) {
			serverDone := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if terminal == "completed" {
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"result\":%q}]}}\n\n", encoded)
				} else {
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"image_generation_call\",\"result\":%q}}\n\ndata: [DONE]\n\n", encoded)
				}
				if err := http.NewResponseController(w).Flush(); err != nil {
					t.Errorf("flush terminal image event: %v", err)
					return
				}
				select {
				case <-r.Context().Done():
				case <-serverDone:
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(serverDone) })
			provider := openaicodex.NewProvider(
				openaicodex.StaticTokenSource{T: openaicodex.Token{Access: "token", AccountID: "acct"}},
				openaicodex.WithBaseURL(server.URL), openaicodex.WithNoRetry(),
			)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			got, err := provider.GenerateImage(ctx, llm.ImageGenerationRequest{Prompt: "tiny test image"})
			if err != nil {
				t.Fatalf("GenerateImage before transport EOF: %v", err)
			}
			if !bytes.Equal(got.Data, pngData) || got.MIMEType != "image/png" {
				t.Fatal("terminal image response was not preserved")
			}
		})
	}
}
