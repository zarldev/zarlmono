package subscribers_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/service"
	"github.com/zarldev/zarlmono/zarlai/subscribers"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

type fakeChatClient struct {
	response string
}

func (f *fakeChatClient) Chat(_ context.Context, msgs []service.Message, _ []llm.Tool) (service.ChatResult, error) {
	return service.ChatResult{Content: f.response}, nil
}

// newTemplates builds a populated PromptTemplateStore for tests
// — mirrors what cmd/zarl does at startup so Summarizer / Extractor
// can render their templates without a DB.
func newTemplates() *service.MemoryPromptTemplateStore {
	s := service.NewMemoryPromptTemplateStore()
	subscribers.RegisterTemplates(s)
	return s
}

type fakeSummaryStore struct {
	stored []struct {
		personName, summary, sessionID string
	}
}

func (f *fakeSummaryStore) CreateSummary(_ context.Context, personName, summary, sessionID string) error {
	f.stored = append(f.stored, struct {
		personName, summary, sessionID string
	}{personName, summary, sessionID})
	return nil
}

func TestSummarizer_stores_summary_on_session_ended(t *testing.T) {
	chat := &fakeChatClient{response: "Discussed kitchen lights and motion triggers."}
	store := &fakeSummaryStore{}
	h := subscribers.NewSummarizer(chat, store, newTemplates())

	err := h.Handle(t.Context(), events.Event{
		Type: events.SessionEnded,
		Payload: events.SessionEndedPayload{
			SessionID:  "sess-1",
			PersonName: "Alice",
			Messages: []events.Message{
				{Role: "user", Content: "Can you set up motion triggers for the kitchen?"},
				{Role: "assistant", Content: "Sure, I'll configure the motion sensor."},
				{Role: "user", Content: "Great, and the hallway too."},
				{Role: "assistant", Content: "Done, both are configured."},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.stored) != 1 {
		t.Fatalf("expected 1 stored summary, got %d", len(store.stored))
	}
	s := store.stored[0]
	if s.personName != "Alice" {
		t.Errorf("expected person Alice, got %s", s.personName)
	}
	if s.summary != "Discussed kitchen lights and motion triggers." {
		t.Errorf("unexpected summary: %s", s.summary)
	}
}

func TestSummarizer_skips_anonymous_sessions(t *testing.T) {
	chat := &fakeChatClient{response: "Summary."}
	store := &fakeSummaryStore{}
	h := subscribers.NewSummarizer(chat, store, newTemplates())

	err := h.Handle(t.Context(), events.Event{
		Type: events.SessionEnded,
		Payload: events.SessionEndedPayload{
			SessionID:  "sess-1",
			PersonName: "",
			Messages:   []events.Message{{Role: "user", Content: "hello"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.stored) != 0 {
		t.Fatalf("expected no stored summaries for anonymous session, got %d", len(store.stored))
	}
}

func TestSummarizer_skips_short_sessions(t *testing.T) {
	chat := &fakeChatClient{response: "Summary."}
	store := &fakeSummaryStore{}
	h := subscribers.NewSummarizer(chat, store, newTemplates())

	err := h.Handle(t.Context(), events.Event{
		Type: events.SessionEnded,
		Payload: events.SessionEndedPayload{
			SessionID:  "sess-1",
			PersonName: "Alice",
			Messages:   []events.Message{{Role: "user", Content: "hi"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.stored) != 0 {
		t.Fatalf("expected no stored summaries for short session, got %d", len(store.stored))
	}
}
