package subscribers_test

import (
	"context"
	"testing"

	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/subscribers"
)

type fakeMemoryStore struct {
	stored []struct {
		personName, fact string
	}
	existing []string
}

func (f *fakeMemoryStore) LoadMemories(_ context.Context, _ string, _ int) ([]string, error) {
	return f.existing, nil
}

func (f *fakeMemoryStore) StoreMemory(_ context.Context, personName, fact string) error {
	f.stored = append(f.stored, struct {
		personName, fact string
	}{personName, fact})
	return nil
}

func TestExtractor_extracts_and_stores_memories(t *testing.T) {
	chat := &fakeChatClient{response: "- Alice is vegetarian\n- Alice works from home on Fridays"}
	store := &fakeMemoryStore{}
	h := subscribers.NewExtractor(chat, store, newTemplates())

	err := h.Handle(t.Context(), events.Event{
		Type: events.SessionEnded,
		Payload: events.SessionEndedPayload{
			SessionID:  "sess-1",
			PersonName: "Alice",
			Messages: []events.Message{
				{Role: "user", Content: "I'm vegetarian by the way."},
				{Role: "assistant", Content: "Good to know! I'll keep that in mind."},
				{Role: "user", Content: "Also I work from home on Fridays."},
				{Role: "assistant", Content: "Noted!"},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.stored) != 2 {
		t.Fatalf("expected 2 stored memories, got %d", len(store.stored))
	}
	if store.stored[0].fact != "Alice is vegetarian" {
		t.Errorf("unexpected fact: %s", store.stored[0].fact)
	}
}

func TestExtractor_skips_anonymous_sessions(t *testing.T) {
	chat := &fakeChatClient{response: "- some fact"}
	store := &fakeMemoryStore{}
	h := subscribers.NewExtractor(chat, store, newTemplates())

	err := h.Handle(t.Context(), events.Event{
		Type: events.SessionEnded,
		Payload: events.SessionEndedPayload{
			PersonName: "",
			Messages:   []events.Message{{Role: "user", Content: "hello"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.stored) != 0 {
		t.Fatal("expected no stored memories for anonymous session")
	}
}
