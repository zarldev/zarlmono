package subscribers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/service"
)

const minMessagesForSummary = 4

// SummaryStore persists conversation summaries.
type SummaryStore interface {
	CreateSummary(ctx context.Context, personName, summary, sessionID string) error
}

// Summarizer generates and stores conversation summaries on session end.
type Summarizer struct {
	chat      service.ChatClient
	store     SummaryStore
	templates service.PromptTemplateStore
}

// NewSummarizer creates a Summarizer handler.
func NewSummarizer(chat service.ChatClient, store SummaryStore, templates service.PromptTemplateStore) *Summarizer {
	return &Summarizer{chat: chat, store: store, templates: templates}
}

// Handle processes a SessionEnded event.
func (s *Summarizer) Handle(ctx context.Context, e events.Event) error {
	p, ok := e.Payload.(events.SessionEndedPayload)
	if !ok {
		return fmt.Errorf("summarizer: unexpected payload type %T", e.Payload)
	}
	if p.PersonName == "" {
		slog.Debug("summarizer: skipping anonymous session")
		return nil
	}
	if len(p.Messages) < minMessagesForSummary {
		slog.Debug("summarizer: skipping short session", "messages", len(p.Messages))
		return nil
	}

	system := s.templates.Render(TemplateSummarizerSystem, map[string]string{
		"person_name": p.PersonName,
	})
	if system == "" {
		return fmt.Errorf("summarizer: template %q missing", TemplateSummarizerSystem)
	}
	msgs := []service.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: "Transcript:\n\n" + serializeConversation(p.Messages)},
	}

	result, err := s.chat.Chat(ctx, msgs, nil)
	if err != nil {
		return fmt.Errorf("summarizer chat: %w", err)
	}

	if err := s.store.CreateSummary(ctx, p.PersonName, result.Content, p.SessionID); err != nil {
		return fmt.Errorf("summarizer store: %w", err)
	}

	slog.Info("summarizer: stored conversation summary", "person", p.PersonName, "session", p.SessionID)
	return nil
}
