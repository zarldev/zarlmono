package compact

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// HandoverDefaultMaxTokens caps the handover document. Larger than the
// executive narrative budget because the handover is the successor's ONLY
// context — it must be self-contained rather than a supplement to recent
// verbatim turns.
const HandoverDefaultMaxTokens = 4000

// HandoverDefaultSystemPrompt instructs the model to write a self-contained
// handover for a fresh agent that will take over with no other context — the
// whole prior conversation is cleared and replaced by this document.
const HandoverDefaultSystemPrompt = `You are writing a HANDOVER DOCUMENT for a fresh coding agent that will take over this task with NO other context: the entire prior conversation is being cleared and replaced by your document. It must be self-contained — if it is not in your document, the successor will not know it.

A PLAN PROGRESS section is already attached above your output — do NOT repeat it. Produce these markdown sections:

## Objective
The user's goal in their own words where possible, and what "done" looks like.

## Done so far
What has been accomplished and actually verified (name the evidence: passing tests, confirmed output).

## Current state
Where things stand right now, and what was mid-flight when the handover was taken.

## Key decisions
Choices already committed to and why (architecture, libraries, patterns), so the successor does not relitigate them.

## Files to read first
The specific file paths the successor should open to re-orient, and why each matters. The successor has no memory of which files are in play — name them explicitly.

## Next steps
The concrete next actions, in order.

## Gotchas
Dead ends already explored, pitfalls, and constraints the successor must respect.

Be concrete and load-bearing. Paraphrase evidence rather than re-quoting large output. Do not invent facts that are not present in the conversation.`

// HandoverWriter persists a produced handover document and returns the path it
// was written to. It is injected so the compact package stays free of file I/O;
// a nil writer simply skips persistence (the document still seeds the context).
type HandoverWriter func(ctx context.Context, doc string) (path string, err error)

// Handover is the clear-and-reseed compactor. Unlike every other engine it does
// NOT keep recent turns: it summarises the ENTIRE conversation into a single
// self-contained handover document, optionally writes it to a file, and returns
// a history of just the leading system messages plus one user-role message
// carrying the document. The effect is a complete context wipe whose only
// surviving context is the handover — the model continues from the document as
// if a fresh successor picked up the task.
//
// keepRecent is ignored: a partial keep would defeat the point (a clean reseed).
type Handover struct {
	Provider     llm.Provider
	Model        string
	State        StateProvider
	Writer       HandoverWriter
	SystemPrompt string // optional override; defaults to HandoverDefaultSystemPrompt
	MaxTokens    int    // optional override; defaults to HandoverDefaultMaxTokens
}

// NewHandover constructs a Handover compactor. provider + model drive the
// document LLM call; state supplies the PLAN PROGRESS section; writer persists
// the document (nil skips the file, keeping the in-context reseed).
func NewHandover(provider llm.Provider, model string, state StateProvider, writer HandoverWriter) *Handover {
	return &Handover{Provider: provider, Model: model, State: state, Writer: writer}
}

// WouldReduceBytes implements [Prober]. Handover collapses the whole
// conversation to one document, so the upper-bound saving is every non-leading
// message's content; the trigger fires whenever there is anything to hand over.
func (h *Handover) WouldReduceBytes(history []llm.Message, _ int) int {
	_, older := splitLeadingSystem(history)
	total := 0
	for _, msg := range older {
		total += len(msg.Content)
	}
	return total
}

// Compact implements [Compactor]. It renders the entire non-system history into
// a handover document, writes it via the injected writer, and returns the
// leading system messages plus a single user message carrying the document.
func (h *Handover) Compact(ctx context.Context, history []llm.Message, _ int) (Result, error) {
	if h.Provider == nil {
		return Result{}, errors.New("compact.Handover: provider is nil")
	}
	leading, older := splitLeadingSystem(history)
	if len(older) == 0 {
		return Result{History: append([]llm.Message{}, history...), Engine: EngineHandover}, nil
	}
	olderBytes := 0
	for _, msg := range older {
		olderBytes += len(msg.Content)
	}

	var plan string
	if h.State != nil {
		plan = renderPlanSection(h.State.Plan())
	}

	sysPrompt := h.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = HandoverDefaultSystemPrompt
	}
	maxTokens := h.MaxTokens
	if maxTokens <= 0 {
		maxTokens = HandoverDefaultMaxTokens
	}

	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: sysPrompt},
			{Role: llm.RoleUser, Content: renderOlderForSummary(older)},
		},
		MaxTokens:   maxTokens,
		Temperature: 0.2,
		Stream:      true,
	}
	stream, err := h.Provider.Complete(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("compact.Handover: provider complete: %w", err)
	}
	var body strings.Builder
	for chunk, cerr := range stream {
		if cerr != nil {
			return Result{}, fmt.Errorf("compact.Handover: stream: %w", cerr)
		}
		body.WriteString(chunk.Content)
	}
	doc := strings.TrimSpace(body.String())
	if doc == "" {
		return Result{}, errors.New("compact.Handover: model returned an empty document")
	}
	doc = composeHandover(plan, doc)

	// Persist the document. A write failure must not lose the reseed — the
	// document is still valid context — so it degrades to a warning note.
	savedNote := ""
	if h.Writer != nil {
		if path, werr := h.Writer(ctx, doc); werr != nil {
			savedNote = fmt.Sprintf("\n\n_(handover not saved to disk: %v)_", werr)
		} else if path != "" {
			savedNote = fmt.Sprintf("\n\n_Handover saved to %s._", path)
		}
	}

	seed := "# Session handover\n\n" +
		"The previous conversation has been cleared. Continue the task using only this handover.\n\n" +
		doc + savedNote

	out := make([]llm.Message, 0, len(leading)+1)
	out = append(out, leading...)
	out = append(out, llm.Message{Role: llm.RoleUser, Content: seed})

	// The whole non-system history collapses to the seed, so the byte saving is
	// the older content minus the seed — reported so the cockpit gauge drops
	// immediately on the wipe rather than waiting for the next usage sample.
	trimmed := max(olderBytes-len(seed), 0)
	warning := fmt.Sprintf("handover: cleared %d message(s) and reseeded from a %d-char handover document", len(older), len(doc))
	return Result{History: out, Warning: warning, Engine: EngineHandover, BytesTrimmed: trimmed}, nil
}

// composeHandover prepends the mechanical PLAN PROGRESS section (elided when
// empty) to the model-produced document body.
func composeHandover(plan, body string) string {
	if plan == "" {
		return body
	}
	return plan + "\n\n" + body
}

// splitLeadingSystem partitions history into the leading run of system messages
// and everything after it. Unlike splitForSummary it keeps nothing recent — the
// handover clears the whole conversation.
func splitLeadingSystem(history []llm.Message) ([]llm.Message, []llm.Message) {
	cut := 0
	for cut < len(history) && history[cut].Role == llm.RoleSystem {
		cut++
	}
	return history[:cut], history[cut:]
}
