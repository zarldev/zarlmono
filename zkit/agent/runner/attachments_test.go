package runner_test

import (
	"context"
	"iter"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type attachmentRecordingProvider struct {
	req llm.CompletionRequest
}

func (p *attachmentRecordingProvider) Complete(_ context.Context, req llm.CompletionRequest) (iter.Seq2[llm.CompletionChunk, error], error) {
	p.req = req
	return func(yield func(llm.CompletionChunk, error) bool) {
		yield(llm.CompletionChunk{Content: "ok", Done: true}, nil)
	}, nil
}

func (p *attachmentRecordingProvider) Name() string { return "attachment-recording" }

func TestRunSendsAttachmentsOnInitialUserMessage(t *testing.T) {
	provider := &attachmentRecordingProvider{}
	r := runner.New(provider)
	res := r.Run(t.Context(), runner.TaskSpec{
		Prompt: "describe this",
		Attachments: []llm.ContentPart{
			llm.ImagePartFromDataURI("data:image/png;base64,abc", "image/png"),
		},
	})
	if res.Err != nil {
		t.Fatalf("Run: %v", res.Err)
	}
	if len(provider.req.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(provider.req.Messages))
	}
	msg := provider.req.Messages[0]
	if msg.Content != "describe this" {
		t.Fatalf("content = %q", msg.Content)
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("parts = %d, want text + image", len(msg.Parts))
	}
	if msg.Parts[0].Type != llm.ContentTypeText || msg.Parts[0].Text != "describe this" {
		t.Fatalf("text part = %#v", msg.Parts[0])
	}
	if msg.Parts[1].Type != llm.ContentTypeImage || msg.Parts[1].Image == nil {
		t.Fatalf("image part = %#v", msg.Parts[1])
	}
}
