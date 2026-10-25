package openai_test

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/openai"
)

type trackedStreamBody struct {
	reader io.Reader
	closed atomic.Bool
}

func (b *trackedStreamBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *trackedStreamBody) Close() error               { b.closed.Store(true); return nil }

func TestChatStreamOwnsBodyOnEarlyStopAndDecodeFailure(t *testing.T) {
	for _, data := range []string{
		"data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"one\"},\"index\":0}]}\n\n",
		"data: malformed-json\n\n",
	} {
		body := &trackedStreamBody{reader: strings.NewReader(data)}
		client := &http.Client{Transport: optionTransport(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: request}, nil
		})}
		provider := openai.NewProvider("synthetic-key", openai.WithModel("gpt-4o"), openai.WithBaseURL("http://fixture.invalid/v1"), openai.WithHTTPClient(client))
		provider.Complete(t.Context(), llm.CompletionRequest{Stream: true, Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}}})(func(llm.CompletionChunk, error) bool { return false })
		if !body.closed.Load() {
			t.Fatal("stream returned without closing its owned response body")
		}
	}
}
