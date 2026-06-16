package compact

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// SummaryDefaultMaxTokens caps the length of the model-produced
// summary. 4k output tokens at the default 4-chars-per-token rate
// equals ~16 KB of summary text — plenty for a working narrative
// while small enough to leave context headroom on a 32k window.
const SummaryDefaultMaxTokens = 4096

// SummaryDefaultSystemPrompt is the instruction the secondary model
// receives. Targeted at coding-agent history: preserves the user's
// stated goals, ongoing tasks, decisions, file paths, and any
// findings the agent will need to reference downstream. The shell's
// settings may override.
const SummaryDefaultSystemPrompt = `You are a conversation summariser. The user message you receive contains the older portion of a coding-agent conversation that is being compacted to free context. Produce a single, terse summary that preserves:

  - The user's stated goals and instructions (in their words where possible).
  - Key decisions, plans, and trade-offs the agent committed to.
  - Important file paths, identifiers, and tool results the agent will reference downstream.
  - Any open tasks, blockers, or follow-ups.

Omit chit-chat, redundant explanations, and full tool-output blobs (note that "the file was read" suffices; the agent can re-read).

Write the summary as third-person prose, with bullet lists where they help readability. Do not exceed ~600 words.`

// Summary is the LLM-driven compactor. It feeds the older portion of
// the conversation to a configured [llm.Provider] (typically a smaller
// / cheaper model than the one running the main loop) and replaces
// those messages with a single assistant turn that carries the
// produced summary text. The most-recent keepRecent messages stay
// verbatim.
//
// Trade-offs vs [Structural]:
//
//   - Pros: ratio is much higher — a 200-msg history might compress
//     to a single 600-word summary, vs structural-trim's ~30 % save.
//   - Cons: tool result bodies are paraphrased, not retained. If the
//     agent later wants "what was on line 42 of foo.go" it must
//     re-read. Use Structural for tool-heavy sessions; Summary when
//     the history is mostly narrative.
//
// Construct with [NewSummary]; pass a built [llm.Provider] (the shell
// builds this via its existing backends.Build → Provider pipeline).
// Model is the model name passed in [llm.CompletionRequest]; an empty
// Model uses the provider's default.
type Summary struct {
	Provider     llm.Provider
	Model        string
	SystemPrompt string // optional override; falls back to SummaryDefaultSystemPrompt
	MaxTokens    int    // optional override; falls back to SummaryDefaultMaxTokens
}

// NewSummary returns a Summary compactor bound to provider. model is
// the model name the provider should use ("gpt-4o-mini",
// "qwen3-coder-30b", etc.); empty falls back to the provider's
// default. The system prompt and max-tokens use the package
// defaults; mutate the returned struct fields to override.
func NewSummary(provider llm.Provider, model string) *Summary {
	return &Summary{Provider: provider, Model: model}
}

// WouldReduceBytes implements [Prober]. Summary replaces every older
// message with a single (much shorter) summary line, so any history
// where there's more than one older message to fold yields work.
// Returns the byte count of the older slice as the upper-bound
// estimate of savings; the actual summary takes some bytes back but
// it's nearly always a net win when the trigger fires.
func (s *Summary) WouldReduceBytes(history []llm.Message, keepRecent int) int {
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(history) <= keepRecent+1 {
		return 0
	}
	older := history[:len(history)-keepRecent]
	total := 0
	for _, msg := range older {
		total += len(msg.Content)
	}
	return total
}

// Compact implements [Compactor]. Splits history at keepRecent, sends
// the older portion to the configured provider, and replaces it with
// a single assistant message carrying the produced summary. The
// leading system message (if any) is preserved verbatim at index 0;
// the user's most-recent keepRecent messages also pass through.
//
// On provider failure the input history is returned unchanged with
// the error — callers (the shell) treat this as a soft failure and
// can fall back to a no-op or a different engine.
func (s *Summary) Compact(ctx context.Context, history []llm.Message, keepRecent int) (Result, error) {
	if s.Provider == nil {
		return Result{}, errors.New("compact.Summary: provider is nil")
	}
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(history) <= keepRecent+1 {
		// Nothing meaningful to summarise (one or zero older messages).
		return Result{
			History: append([]llm.Message{}, history...),
			Engine:  EngineSummary,
		}, nil
	}

	// Carve the slice: leading system msg(s), older to summarise,
	// most-recent kept verbatim.
	leading, older, recent := splitForSummary(history, keepRecent)
	if len(older) == 0 {
		return Result{
			History: append([]llm.Message{}, history...),
			Engine:  EngineSummary,
		}, nil
	}

	sysPrompt := s.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = SummaryDefaultSystemPrompt
	}
	maxTokens := s.MaxTokens
	if maxTokens <= 0 {
		maxTokens = SummaryDefaultMaxTokens
	}

	rendered := renderOlderForSummary(older)
	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: sysPrompt},
			{Role: llm.RoleUser, Content: rendered},
		},
		MaxTokens:   maxTokens,
		Temperature: 0.2,
		Stream:      true,
	}
	stream, err := s.Provider.Complete(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("compact.Summary: provider complete: %w", err)
	}

	var summary strings.Builder
	for chunk, cerr := range stream {
		if cerr != nil {
			return Result{}, fmt.Errorf("compact.Summary: stream: %w", cerr)
		}
		summary.WriteString(chunk.Content)
	}
	body := strings.TrimSpace(summary.String())
	if body == "" {
		return Result{}, errors.New("compact.Summary: model returned empty summary")
	}

	out := make([]llm.Message, 0, len(leading)+1+len(recent))
	out = append(out, leading...)
	out = append(out, llm.Message{
		Role: llm.RoleAssistant,
		Content: fmt.Sprintf(
			"[compacted — summary of %d older message(s)]\n\n%s",
			len(older), body),
	})
	out = append(out, recent...)

	// Defensive sweep: the snap above keeps tool messages glued to
	// their owning assistant, but a previous compaction (or a
	// restored session) can still leave behind tool results whose
	// call_id no longer exists in the trimmed history — or a recent
	// assistant tool_use whose result fell on the other side of the
	// cut. Repair both directions before handing the slice back —
	// OpenAI / Codex / llama.cpp reject either orphan with a 400 the
	// runner can't recover from.
	out, _ = RepairToolPairing(out)

	// BytesTrimmed for summary is the size of the older slice we
	// replaced (minus what we wrote back). This is approximate: it
	// counts the raw byte savings the engine produced, ignoring
	// token accounting.
	var olderBytes int
	for _, m := range older {
		olderBytes += len(m.Content)
	}
	trimmed := olderBytes - len(body)
	if trimmed < 0 {
		trimmed = 0
	}
	warning := fmt.Sprintf("compacted via summary: %d older msgs → 1 summary msg (~%d chars); %d recent kept verbatim",
		len(older), len(body), len(recent))
	return Result{History: out, Warning: warning, Engine: EngineSummary, BytesTrimmed: trimmed}, nil
}

// splitForSummary carves history into (leading, older, recent) where
// leading is any consecutive system messages at the head, recent is
// the tail keepRecent messages, and older is everything between.
//
// Turn-boundary snapping: the naive tail = len-keepRecent cut can
// land on a role=llm.RoleTool message whose tool_call_id was emitted by an
// assistant message just before it. If we summarise that assistant
// away, the recent slice ships a tool result whose anchoring call
// no longer exists, and the LLM API returns:
//
//	No tool call found for function call output with call_id call_…
//
// To keep tool_call_id linkage valid across the boundary we walk
// tail backward while history[tail] is a tool message, pulling the
// owning assistant turn into recent. The slice grows by at most the
// width of one tool batch — a small cost that's cheaper than re-
// executing the broken turn.
func splitForSummary(history []llm.Message, keepRecent int) ([]llm.Message, []llm.Message, []llm.Message) {
	var leading, older, recent []llm.Message
	cut := 0
	for cut < len(history) && history[cut].Role == llm.RoleSystem {
		cut++
	}
	leading = history[:cut]
	tail := len(history) - keepRecent
	if tail < cut {
		tail = cut
	}
	// Bounds clamp: keepRecent == 0 lands tail at len(history); the
	// snap loop below dereferences history[tail], which would panic
	// in that case. Drop to len-1 so the loop has something to look
	// at, and rely on the snap to back up further when needed.
	if tail >= len(history) && tail > cut {
		tail = len(history) - 1
	}
	// Snap tail backward to the most recent non-tool message so any
	// tool responses at the head of `recent` retain their anchoring
	// assistant message. Stops at `cut` so leading system messages
	// don't get pulled in.
	for tail > cut && history[tail].Role == llm.RoleTool {
		tail--
	}
	older = history[cut:tail]
	recent = history[tail:]
	return leading, older, recent
}

// PruneOrphanToolResults walks history left-to-right and drops any
// llm.RoleTool message whose ToolCallID isn't claimed by a preceding
// assistant message's ToolCalls. The function exists because the
// snap in splitForSummary handles the easy case (tool at the head
// of recent) but the runner's history can carry orphans for other
// reasons too: a previous compaction summarised an assistant's
// ToolCalls into prose, an llm.Provider hallucinated a result, a
// session-restore reconstructed a partial transcript.
//
// Without this sweep, OpenAI / Codex / llama.cpp's OAI shim respond
// with `No tool call found for function call output with call_id …`
// 400 errors that the runner cannot recover from without dropping
// the offender.
//
// Returns a filtered slice and the count of dropped messages. The
// input is not mutated. Collapse-style compactors (Summary,
// Executive) call this automatically before returning; pure-trim
// compactors (Structural) don't, since they can't introduce
// orphans themselves — but consumers that route a Structural
// result back into the runner can call this explicitly if they
// suspect the upstream history was already poisoned.
func PruneOrphanToolResults(history []llm.Message) ([]llm.Message, int) {
	known := map[string]bool{}
	var out []llm.Message
	dropped := 0
	out = make([]llm.Message, 0, len(history))
	for _, m := range history {
		if m.Role == llm.RoleAssistant {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					known[tc.ID] = true
				}
			}
			out = append(out, m)
			continue
		}
		if m.Role == llm.RoleTool {
			if m.ToolCallID == "" || !known[m.ToolCallID] {
				dropped++
				continue
			}
		}
		out = append(out, m)
	}
	return out, dropped
}

// RepairToolPairing makes history satisfy the provider tool-call contract in
// BOTH directions. PruneOrphanToolResults only handles one side (a tool result
// with no preceding assistant call); this also strips assistant tool-call
// entries that never receive a result. A dangling assistant tool_use block
// makes Anthropic (and several OpenAI-compatible backends) reject the whole
// request with a 400, so a session-restore of a partially-written or
// externally-mutated transcript would otherwise brick every turn.
//
// It first drops orphan results (so every surviving result has a preceding
// call), then strips any assistant ToolCalls whose ID is never answered by a
// surviving result. An assistant message left with no tool calls and no
// content (visible or reasoning) is dropped entirely. Returns the repaired
// slice and the total count of dropped messages plus stripped tool-call
// entries. The input is not mutated.
func RepairToolPairing(history []llm.Message) ([]llm.Message, int) {
	pruned, changed := PruneOrphanToolResults(history)

	answered := map[string]bool{}
	for _, m := range pruned {
		if m.Role == llm.RoleTool && m.ToolCallID != "" {
			answered[m.ToolCallID] = true
		}
	}

	out := make([]llm.Message, 0, len(pruned))
	for _, m := range pruned {
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			out = append(out, m)
			continue
		}
		kept := make([]llm.ToolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			if tc.ID != "" && answered[tc.ID] {
				kept = append(kept, tc)
			}
		}
		if len(kept) == len(m.ToolCalls) {
			out = append(out, m)
			continue
		}
		changed += len(m.ToolCalls) - len(kept)
		if len(kept) == 0 && strings.TrimSpace(m.Content) == "" && strings.TrimSpace(m.ReasoningContent) == "" {
			// Nothing meaningful left — an assistant turn that was only its
			// (now unanswered) tool calls.
			continue
		}
		m.ToolCalls = kept
		out = append(out, m)
	}
	return out, changed
}

// renderOlderForSummary serialises the older messages into a single
// text blob the secondary model can consume. Role-tagged so the
// summariser can distinguish user intent from tool output.
func renderOlderForSummary(older []llm.Message) string {
	var b strings.Builder
	for i, m := range older {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s]\n", roleLabel(m.Role))
		if m.Content != "" {
			b.WriteString(m.Content)
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "\n  → tool_call: %s", tc.Function.Name)
			}
		}
	}
	return b.String()
}

func roleLabel(role string) string {
	switch role {
	case llm.RoleUser:
		return "USER"
	case llm.RoleAssistant:
		return "ASSISTANT"
	case llm.RoleSystem:
		return "SYSTEM"
	case llm.RoleTool:
		return "TOOL_RESULT"
	}
	return strings.ToUpper(role)
}
