package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const recentWindowSize = 10

// Session holds per-connection conversation state.
type Session struct {
	history           []Message
	identity          string
	pendingEnrollment []float32
	pendingPhoto      string
	askedForName      bool
	latitude          float64
	longitude         float64
}

func NewSession(systemPrompt string) *Session {
	return &Session{
		history: []Message{
			{Role: "system", Content: systemPrompt},
		},
	}
}

// History returns the session's full message log including every image
// ever captured. Used by persistence and session-trimming logic that
// needs the raw record. For LLM prompting use HistoryForChat instead,
// which strips stale camera images.
func (s *Session) History() []Message { return s.history }

// HistoryForChat returns a copy of the session history where images are
// kept only on the CHRONOLOGICALLY latest user message — even if that
// message has no image (e.g. the frontend deduped a near-identical
// frame). Earlier "latest user message WITH images" semantics caused
// identity drift: when dedup skipped a turn, an image from several
// turns back stayed attached, and the LLM would anchor on that visual
// (potentially showing a different person) rather than trust the
// system-prompt identity the face recogniser maintains live.
//
// Stripping is done on a copy so the authoritative history (for
// persistence / summarisation) is untouched.
func (s *Session) HistoryForChat() []Message {
	latestUserIdx := -1
	for i := len(s.history) - 1; i >= 0; i-- {
		if s.history[i].Role == "user" {
			latestUserIdx = i
			break
		}
	}
	if latestUserIdx < 0 {
		return s.history
	}
	out := make([]Message, len(s.history))
	for i, m := range s.history {
		if i != latestUserIdx && len(m.Images) > 0 {
			m.Images = nil
		}
		out[i] = m
	}
	return out
}

func (s *Session) Identity() string { return s.identity }

// Identify records the recognized person for this session. An empty id means
// "no known person" — useful when a face disappears from the frame.
func (s *Session) Identify(id string) { s.identity = id }

func (s *Session) HasPendingEnrollment() bool   { return s.pendingEnrollment != nil }
func (s *Session) PendingEnrollment() []float32 { return s.pendingEnrollment }

// PendEnrollment stashes a face embedding waiting on the user's name.
func (s *Session) PendEnrollment(e []float32) { s.pendingEnrollment = e }

func (s *Session) PendingPhoto() string { return s.pendingPhoto }

// PendPhoto stashes a base64 photo alongside a pending enrollment.
func (s *Session) PendPhoto(p string) { s.pendingPhoto = p }

func (s *Session) HasAskedForName() bool { return s.askedForName }

// AskForName marks that the assistant has asked the current unknown face
// for a name — used to avoid re-asking on every turn.
func (s *Session) AskForName() { s.askedForName = true }

// ResetAskedForName clears the asked-for-name flag when a new unknown face
// appears or enrollment completes.
func (s *Session) ResetAskedForName() { s.askedForName = false }

// Location returns the browser-supplied coordinates for this session.
// Zero value means "not provided" — callers should check Coordinates.Known().
func (s *Session) Location() Coordinates {
	return Coordinates{Lat: s.latitude, Lng: s.longitude}
}

// LocateAt records the user's coordinates; used for location-aware responses.
func (s *Session) LocateAt(lat, lng float64) { s.latitude = lat; s.longitude = lng }

// SetSystemPrompt replaces the system message at the head of the history.
// Used by the Converse path to re-render placeholder substitutions each
// turn without requiring operators to edit the stored template.
func (s *Session) SetSystemPrompt(prompt string) {
	if len(s.history) > 0 && s.history[0].Role == "system" {
		s.history[0].Content = prompt
		return
	}
	s.history = append([]Message{{Role: "system", Content: prompt}}, s.history...)
}

func (s *Session) ClearEnrollment() {
	s.pendingEnrollment = nil
	s.pendingPhoto = ""
	s.askedForName = false
}

func (s *Session) AddUser(content string, images []string) {
	s.history = append(s.history, Message{
		Role:    "user",
		Content: content,
		Images:  images,
	})
}

func (s *Session) AddAssistant(content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	s.history = append(s.history, Message{
		Role:    "assistant",
		Content: content,
	})
}

// AddAssistantWithToolCalls appends an assistant message that includes tool calls.
func (s *Session) AddAssistantWithToolCalls(content string, toolCalls []ToolCall) {
	if strings.TrimSpace(content) == "" && len(toolCalls) == 0 {
		return
	}
	s.history = append(s.history, Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	})
}

// AddToolResult appends a tool response message.
func (s *Session) AddToolResult(content string) {
	s.history = append(s.history, Message{
		Role:    "tool",
		Content: content,
	})
}

// BuildUserContent constructs the user message content from available inputs.
// The image, when present, is attached as a vision input on the message —
// we don't prompt the model to "describe what you see" because that turned
// every turn into a scene-acknowledgement ("I see you in your office, ...").
// The model integrates the image automatically when it's relevant.
func BuildUserContent(transcription string, hasImage bool, text string) string {
	var parts []string
	_ = hasImage // image is carried as a separate field on the message, not narrated here

	if transcription != "" {
		parts = append(parts, `The user said: "`+transcription+`"`)
	}

	if text != "" {
		parts = append(parts, `The user said: "`+text+`"`)
	}

	if len(parts) == 0 {
		return "Hello!"
	}

	return strings.Join(parts, " ")
}

// imageTokenCost is our per-image estimate — matches the llama-server
// `--image-max-tokens` cap so accumulated camera frames aren't invisible
// to the budget calculation.
const imageTokenCost = 1024

// keepImagesOnRecent is the number of most-recent messages that retain
// their images during trimming. The agent only needs fresh visual
// context; stale frames from turns ago cost tokens without adding value.
const keepImagesOnRecent = 2

// EstimateTokens returns a rough token count for all messages in
// history. Text is estimated at 4 chars/token; each attached image
// adds imageTokenCost; each tool call serializes its name + JSON args
// onto the wire even though m.Content is empty for those turns, so we
// count those too. Without them, tool-heavy histories read as zero
// tokens and the trim budget never fires.
func (s *Session) EstimateTokens() int {
	total := 0
	for _, m := range s.history {
		total += len(m.Content) / 4
		total += len(m.Images) * imageTokenCost
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Name) / 4
			if j, err := json.Marshal(tc.Function.Arguments); err == nil {
				total += len(j) / 4
			}
			total += 8 // wire overhead per call (id, type, wrapping braces)
		}
	}
	return total
}

// EstimateToolSpecsTokens returns a rough token cost for the tools
// array that ships on every Chat request. Each registered tool
// contributes its name + description + JSON schema to the prompt,
// invisible to EstimateTokens because tools aren't in history. With
// dozens of tools registered this can approach 10k tokens — enough to
// silently overflow a 32k-per-slot context window when combined with
// recent history. Callers subtract this from the trim budget so
// TrimWithSummary fires against the real available space.
func EstimateToolSpecsTokens(specs []llm.Tool) int {
	total := 0
	for _, s := range specs {
		total += len(s.Function.Name) / 4
		total += len(s.Function.Description) / 4
		if j, err := json.Marshal(s.Function.Parameters); err == nil {
			total += len(j) / 4
		}
		total += 10 // wrapping JSON per tool entry
	}
	return total
}

// TrimWithSummary compresses the session when it exceeds the token
// budget. It runs two strategies in order of cheapness:
//
//  1. Image decay — strip Images from every message except the
//     keepImagesOnRecent most-recent ones. Visual context goes stale
//     fast; old frames eat tokens without adding value.
//  2. Summary compaction — if still over budget after step 1, replace
//     the middle of history (between the first two protected messages
//     and the recentWindowSize most-recent) with a single summary
//     assistant message produced by chat.
//
// The system prompt (index 0), the initial task prompt (index 1), and
// the most recent recentWindowSize messages are always preserved.
func (s *Session) TrimWithSummary(ctx context.Context, chat ChatClient, budget int) error {
	if s.EstimateTokens() <= budget {
		return nil
	}

	// Step 1: image decay. Cheap, no LLM call.
	if len(s.history) > keepImagesOnRecent {
		stripUntil := len(s.history) - keepImagesOnRecent
		for i := range stripUntil {
			s.history[i].Images = nil
		}
	}
	if s.EstimateTokens() <= budget {
		return nil
	}

	const protected = 2 // system prompt + initial task prompt

	if len(s.history) <= protected+recentWindowSize {
		return nil
	}

	recentStart := len(s.history) - recentWindowSize
	// Never start the recent window on an orphan tool result: walk back
	// across any leading tool messages so the paired assistant(tool_calls)
	// stays with them. Without this, the summary eats the assistant turn
	// and the next call ships a tool message with no matching tool_call_id.
	for recentStart > protected && s.history[recentStart].Role == "tool" {
		recentStart--
	}
	middle := s.history[protected:recentStart]

	var sb strings.Builder
	for _, m := range middle {
		fmt.Fprintf(&sb, "%s: %s", m.Role, m.Content)
		if len(m.ToolCalls) > 0 {
			calls := make([]string, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				args, _ := json.Marshal(tc.Function.Arguments)
				calls = append(calls, fmt.Sprintf("%s(%s)", tc.Function.Name, args))
			}
			if m.Content != "" {
				sb.WriteString(" ")
			}
			fmt.Fprintf(&sb, "[tool_calls: %s]", strings.Join(calls, ", "))
		}
		sb.WriteString("\n")
	}

	systemMsg := Message{Role: "system", Content: "Summarize this conversation history concisely. Preserve key facts, tool results, and decisions. Omit redundant details."}
	userMsg := Message{Role: "user", Content: sb.String()}

	result, err := chat.Chat(ctx, []Message{systemMsg, userMsg}, nil)
	if err != nil {
		return fmt.Errorf("TrimWithSummary: %w", err)
	}

	summary := Message{Role: "assistant", Content: "[Summary of earlier work]: " + result.Content}

	rebuilt := make([]Message, 0, protected+1+recentWindowSize)
	rebuilt = append(rebuilt, s.history[:protected]...)
	rebuilt = append(rebuilt, summary)
	rebuilt = append(rebuilt, s.history[recentStart:]...)
	s.history = rebuilt

	return nil
}
