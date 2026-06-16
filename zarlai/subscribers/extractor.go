package subscribers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/zarldev/zarlmono/zarlai/events"
	"github.com/zarldev/zarlmono/zarlai/service"
)

// MemoryStore loads and persists person memories.
type MemoryStore interface {
	LoadMemories(ctx context.Context, personName string, limit int) ([]string, error)
	StoreMemory(ctx context.Context, personName, fact string) error
}

// Extractor proactively extracts facts from conversations and stores them as memories.
type Extractor struct {
	chat      service.ChatClient
	store     MemoryStore
	templates service.PromptTemplateStore
}

// NewExtractor creates an Extractor handler.
func NewExtractor(chat service.ChatClient, store MemoryStore, templates service.PromptTemplateStore) *Extractor {
	return &Extractor{chat: chat, store: store, templates: templates}
}

// Handle processes a SessionEnded event.
func (x *Extractor) Handle(ctx context.Context, e events.Event) error {
	p, ok := e.Payload.(events.SessionEndedPayload)
	if !ok {
		return fmt.Errorf("extractor: unexpected payload type %T", e.Payload)
	}
	if p.PersonName == "" {
		slog.Debug("extractor: skipping anonymous session")
		return nil
	}
	if len(p.Messages) < minMessagesForSummary {
		slog.Debug("extractor: skipping short session", "messages", len(p.Messages))
		return nil
	}

	existing, err := x.store.LoadMemories(ctx, p.PersonName, 50)
	if err != nil {
		return fmt.Errorf("extractor load memories: %w", err)
	}

	var alreadyKnown string
	if len(existing) > 0 {
		var b strings.Builder
		b.WriteString("\n\nAlready known:\n")
		for _, m := range existing {
			b.WriteString("- ")
			b.WriteString(m)
			b.WriteString("\n")
		}
		alreadyKnown = b.String()
	}
	prompt := x.templates.Render(TemplateExtractorSystem, map[string]string{
		"person_name":         p.PersonName,
		"already_known_block": alreadyKnown,
	})
	if prompt == "" {
		return fmt.Errorf("extractor: template %q missing", TemplateExtractorSystem)
	}

	msgs := []service.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: "Transcript:\n\n" + serializeConversation(p.Messages)},
	}

	result, err := x.chat.Chat(ctx, msgs, nil)
	if err != nil {
		return fmt.Errorf("extractor chat: %w", err)
	}

	facts := parseFacts(result.Content)
	for _, fact := range facts {
		if err := x.store.StoreMemory(ctx, p.PersonName, fact); err != nil {
			slog.Error("extractor: store memory failed", "person", p.PersonName, "error", err)
			continue
		}
	}

	slog.Info("extractor: stored memories", "person", p.PersonName, "count", len(facts))
	return nil
}

func parseFacts(content string) []string {
	var facts []string
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "- "); ok {
			fact := after
			fact = strings.TrimSpace(fact)
			if fact != "" {
				facts = append(facts, fact)
			}
		}
	}
	return facts
}
