package compact

import (
	"cmp"
	"context"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// AssistantContentTrimAt is the byte threshold above which an
// older assistant message's narrative Content gets truncated.
// Short assistant turns (acknowledgements, brief plans) stay
// verbatim; long reasoning blocks past this size get trimmed.
const AssistantContentTrimAt = 1024

// ToolResultTrimAt is the byte threshold above which an older tool
// result body gets replaced with the elision marker. Below the
// threshold, the result is small enough to keep verbatim ("ok",
// "wrote 142 bytes", small JSON responses).
const ToolResultTrimAt = 512

// elisionTail is appended to assistant Content when the original
// was longer than the trim threshold — keeps the first chunk (which
// usually carries the gist) and elides the rest. Uses the canonical
// "[compacted — …]" shape so the model recognises it as the same
// signal family as Summary / Executive bridge messages.
const elisionTail = "\n…[compacted — assistant content trimmed; re-run the originating tool if you need the rest]"

const (
	toolAttachmentElision   = "[compacted — tool attachments elided; re-run the originating tool if you need them]"
	toolContentElisionStart = "[compacted — tool result elided; original was ~"
	toolContentElisionEnd   = " bytes. Re-run the tool or `read` the path if you need the content again.]"
)

// Structural is the default compactor. It never calls a model: it
// walks the older portion of the conversation, replacing bulky
// payload bodies (long assistant explanations, large tool result
// blobs) with one-line elision markers while preserving the
// structural metadata (user intent, tool_call_ids that link results
// back to calls, the leading system message). The agent can re-run
// a tool or re-read a file if it needs elided content; the
// conversational thread it uses to plan stays coherent.
//
// Invariants:
//   - User messages: never trimmed (load-bearing intent).
//   - Assistant messages with tool_calls: tool_calls preserved
//     verbatim, narrative Content truncated when long.
//   - Assistant text-only messages: truncated when long.
//   - Tool result messages: oversized Content is replaced with a one-line
//     elision marker; attachments are removed from older messages with a
//     visible marker. ToolCallID is preserved so the call→result link stays
//     valid.
//   - The most-recent `keepRecent` messages stay verbatim.
//
// The two trim thresholds are configurable: zero values fall back
// to the package defaults ([AssistantContentTrimAt] /
// [ToolResultTrimAt]) so the zero-value Struct stays usable as
// before, while a caller that wants to be more aggressive (small-
// context model on tight memory) or more conservative (wide-context
// frontier model where keeping the prose intact reads better) can
// set the fields explicitly.
type Structural struct {
	// AssistantTrimAt overrides the byte threshold above which an
	// older assistant message's narrative Content gets truncated.
	// Zero falls back to [AssistantContentTrimAt] (1024).
	AssistantTrimAt int

	// ToolTrimAt overrides the byte threshold above which an older
	// tool-result Content gets replaced with the elision marker.
	// Zero falls back to [ToolResultTrimAt] (512).
	ToolTrimAt int
}

// NewStructural returns the default-configured Structural compactor.
// The returned value uses the package-default thresholds; mutate
// the AssistantTrimAt / ToolTrimAt fields if you want overrides.
func NewStructural() Structural { return Structural{} }

// assistantTrimAt returns the effective threshold for assistant
// messages — the field when set, else the package default.
func (s Structural) assistantTrimAt() int {
	return cmp.Or(s.AssistantTrimAt, AssistantContentTrimAt)
}

// toolTrimAt returns the effective threshold for tool messages.
func (s Structural) toolTrimAt() int {
	return cmp.Or(s.ToolTrimAt, ToolResultTrimAt)
}

// Compact implements [Compactor].
func (s Structural) Compact(_ context.Context, history []llm.Message, keepRecent int) (Result, error) {
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(history) <= keepRecent {
		return Result{
			History: llm.CloneMessages(history),
			Engine:  EngineStructural,
		}, nil
	}
	older := history[:len(history)-keepRecent]
	recent := history[len(history)-keepRecent:]

	var trimmedBytes int
	out := make([]llm.Message, 0, len(older)+len(recent))
	for _, msg := range older {
		msg = msg.Clone()
		trimmed, saved := s.structuralTrim(msg)
		out = append(out, trimmed)
		trimmedBytes += saved
	}
	out = append(out, llm.CloneMessages(recent)...)

	var warning string
	if trimmedBytes == 0 {
		warning = fmt.Sprintf(
			"compacted: %d older msgs reviewed, nothing large enough to elide; %d recent kept verbatim",
			len(older),
			len(recent),
		)
	} else {
		warning = fmt.Sprintf("compacted: trimmed ~%d bytes from %d older msg(s); %d recent kept verbatim",
			trimmedBytes, len(older), len(recent))
	}
	return Result{History: out, Warning: warning, Engine: EngineStructural, BytesTrimmed: trimmedBytes}, nil
}

// WouldReduceBytes implements [Prober]. It cheaply estimates the bytes Compact
// would save on history with the given keepRecent; zero means "nothing
// trimmable, don't bother firing." The scan does not allocate.
func (s Structural) WouldReduceBytes(history []llm.Message, keepRecent int) int {
	if keepRecent < 0 {
		keepRecent = 0
	}
	if len(history) <= keepRecent {
		return 0
	}
	older := history[:len(history)-keepRecent]
	var savable int
	for _, msg := range older {
		switch msg.Role {
		case llm.RoleAssistant:
			if len(msg.Content) > s.assistantTrimAt() {
				head := max(s.assistantTrimAt()-len(elisionTail), 64)
				savable += max(len(msg.Content)-(head+len(elisionTail)), 0)
			}
		case llm.RoleTool:
			savable += s.planToolTrim(msg).saved
		}
	}
	return savable
}

type toolTrimPlan struct {
	content     bool
	attachments bool
	saved       int
}

func (s Structural) planToolTrim(msg llm.Message) toolTrimPlan {
	partsBytes := llm.ContentPartsByteLen(msg.Parts)
	before := len(msg.Content) + partsBytes
	afterContent := len(msg.Content)
	plan := toolTrimPlan{}
	if len(msg.Content) > s.toolTrimAt() {
		candidate := len(toolContentElisionStart) + decimalDigits(len(msg.Content)) + len(toolContentElisionEnd)
		if candidate < afterContent {
			afterContent = candidate
			plan.content = true
		}
	}
	afterParts := partsBytes
	if len(msg.Parts) > 0 {
		markerBytes := len(toolAttachmentElision)
		if afterContent > 0 {
			markerBytes++
		}
		if markerBytes < afterParts {
			afterContent += markerBytes
			afterParts = 0
			plan.attachments = true
		}
	}
	plan.saved = before - afterContent - afterParts
	return plan
}

func decimalDigits(n int) int {
	digits := 1
	for n >= 10 {
		n /= 10
		digits++
	}
	return digits
}

// structuralTrim returns a copy of msg with bulky content elided
// according to its role. Returns the trimmed message and the number
// of bytes saved. Uses the receiver's configured per-role thresholds
// (AssistantTrimAt / ToolTrimAt) so a Structural with overridden
// fields actually honours those overrides — earlier shape was a
// free function reading package-level constants directly, which
// made the fields decorative.
func (s Structural) structuralTrim(msg llm.Message) (llm.Message, int) {
	out := msg
	switch msg.Role {
	case llm.RoleUser:
		// User intent is load-bearing. Never elide.
		return out, 0
	case llm.RoleAssistant:
		threshold := s.assistantTrimAt()
		if len(msg.Content) <= threshold {
			return out, 0
		}
		head := max(threshold-len(elisionTail), 64)
		content := clipToRune(msg.Content, head) + elisionTail
		if len(content) >= len(msg.Content) {
			return out, 0
		}
		out.Content = content
		return out, len(msg.Content) - len(out.Content)
	case llm.RoleTool:
		plan := s.planToolTrim(msg)
		if plan.content {
			out.Content = fmt.Sprintf("%s%d%s", toolContentElisionStart, len(msg.Content), toolContentElisionEnd)
		}
		if plan.attachments {
			if out.Content != "" {
				out.Content += "\n"
			}
			out.Content += toolAttachmentElision
			out.Parts = nil
		}
		return out, plan.saved
	}
	return out, 0
}
