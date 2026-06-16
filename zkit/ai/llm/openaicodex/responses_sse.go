package openaicodex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// sseEventEnvelope is the minimum shape an SSE event's data field
// needs to dispatch on `type`. Per-type structs further down extract
// the fields each handler cares about.
type sseEventEnvelope struct {
	Type string `json:"type"`
}

type sseTextDelta struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type sseReasoningDelta struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

type sseFunctionArgsDelta struct {
	Type      string `json:"type"`
	Delta     string `json:"delta"`
	ItemID    string `json:"item_id"`
	OutputIdx int    `json:"output_index"`
}

// sseFunctionArgsDone carries the COMPLETE arguments string for a function
// call. The backend sends it after the deltas (so it's usually redundant), but
// some responses deliver the arguments only here with no preceding deltas — in
// that case it's the sole source of the call's arguments.
type sseFunctionArgsDone struct {
	Type      string `json:"type"`
	Arguments string `json:"arguments"`
	ItemID    string `json:"item_id"`
	OutputIdx int    `json:"output_index"`
}

type sseOutputItemAdded struct {
	Type      string        `json:"type"`
	OutputIdx int           `json:"output_index"`
	Item      sseOutputItem `json:"item"`
}

type sseOutputItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type sseCompleted struct {
	Type     string      `json:"type"`
	Response sseResponse `json:"response"`
}

type sseResponse struct {
	Status string   `json:"status"`
	Usage  sseUsage `json:"usage"`
}

type sseUsage struct {
	InputTokens        int                       `json:"input_tokens"`
	OutputTokens       int                       `json:"output_tokens"`
	TotalTokens        int                       `json:"total_tokens"`
	InputTokensDetails sseUsageInputTokenDetails `json:"input_tokens_details"`
}

type sseUsageInputTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type sseFailed struct {
	Type     string `json:"type"`
	Response struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	} `json:"response"`
}

// parseSSEStream reads SSE events from r, translates each into one or
// more llm.CompletionChunk, and yields them until the stream completes,
// fails, or the consumer breaks (yield returns false).
//
// The Responses API streams a richer set of events than Chat
// Completions; we collapse them into the existing chunk shape:
//
//   - text deltas         → CompletionChunk.Content
//   - reasoning deltas    → CompletionChunk.Thinking — the runner's
//     out-of-band reasoning channel, kept disjoint from Content.
//   - function_call adds  → bootstraps a ToolCall with its CallID/Name;
//     subsequent function_call_arguments.delta events accumulate args
//     onto the same call.
//   - response.completed  → emits a final chunk with Done=true,
//     finish_reason, and Usage.
//   - response.failed     → emits a chunk with Error set.
//
// The function returns nil when the stream completes cleanly; an error
// when the connection drops mid-stream. SSE protocol errors (malformed
// data lines) are surfaced as the yield error value and the parser
// continues — one bad event shouldn't kill the whole response.
func parseSSEStream(r io.Reader, yield func(llm.CompletionChunk, error) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var dataBuf strings.Builder
	state := newSSEState()
	// CODEX_DEBUG_SSE=<path> tees every SSE payload to a file so the
	// user can inspect what event types the backend is actually
	// emitting. Useful when reasoning chunks aren't appearing on the
	// Thinking channel — diff the dump against the recognised event
	// list in dispatch() to find missing handlers. Empty / unset =
	// no-op, no I/O cost.
	debugSSE := openSSEDebugSink()
	defer debugSSE.Close()
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// Blank line — event boundary. Dispatch what we've got.
			payload := dataBuf.String()
			dataBuf.Reset()
			if payload == "" {
				continue
			}
			debugSSE.WriteEvent(payload)
			if state.dispatch(payload, yield) {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment / keepalive.
			continue
		}
		if strings.HasPrefix(line, "data:") {
			// SSE may emit multi-line data: blocks. Concatenate them
			// in order; the dispatch reads the joined JSON.
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		}
		// Other SSE fields ("event:", "id:", "retry:") are advisory —
		// we dispatch entirely on the JSON `type` field.
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sse scan: %w", err)
	}
	// A final event may sit in dataBuf with no trailing blank line —
	// happens when the upstream closes the connection right after the
	// last event payload. Dispatch it before flushing.
	if dataBuf.Len() > 0 {
		if state.dispatch(dataBuf.String(), yield) {
			return nil
		}
	}
	// Stream ended without a response.completed — emit a synthetic
	// Done chunk so downstream consumers don't hang.
	state.flush(yield)
	return nil
}

// sseState carries the cross-event state the dispatcher needs: the
// per-output-index tool-call ID map.
type sseState struct {
	// toolCallByIdx tracks output_index → in-flight tool call state.
	// function_call_arguments.delta events arrive identified by
	// item_id only; we use it to look up the call we started in the
	// output_item.added event.
	toolCallByIdx map[int]*pendingToolCall
	toolCallByID  map[string]*pendingToolCall
}

type pendingToolCall struct {
	id        string
	name      string
	arguments strings.Builder
}

func newSSEState() *sseState {
	return &sseState{
		toolCallByIdx: map[int]*pendingToolCall{},
		toolCallByID:  map[string]*pendingToolCall{},
	}
}

// dispatch decodes one event payload and yields chunks. Returns done=true
// when the stream's terminal event has been processed, or when the
// consumer breaks (yield returns false) and iteration must stop.
func (s *sseState) dispatch(payload string, yield func(llm.CompletionChunk, error) bool) bool {
	var env sseEventEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return !yield(llm.CompletionChunk{}, fmt.Errorf("sse decode envelope: %w", err))
	}
	switch env.Type {
	case "response.output_text.delta":
		var ev sseTextDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, fmt.Errorf("sse output_text.delta: %w", err))
		}
		if ev.Delta != "" {
			if !yield(llm.CompletionChunk{Content: ev.Delta}, nil) {
				return true
			}
		}
	// Reasoning event types the Codex backend can emit. Order:
	//   response.reasoning_summary_text.delta    detailed-summary mode
	//   response.reasoning.delta                  legacy summary stream
	//   response.reasoning_text.delta             raw reasoning text
	//                                             (some ChatGPT-account
	//                                             models bypass the
	//                                             summary entirely)
	//   response.reasoning_part.added             part metadata; ignored
	// All variants funnel through the same handler and route to the
	// runner's out-of-band Thinking channel.
	case "response.reasoning_summary_text.delta",
		"response.reasoning.delta",
		"response.reasoning_text.delta",
		"response.reasoning_summary.delta":
		var ev sseReasoningDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, fmt.Errorf("sse reasoning.delta: %w", err))
		}
		if ev.Delta == "" {
			return false
		}
		if !yield(llm.CompletionChunk{Thinking: ev.Delta}, nil) {
			return true
		}
	case "response.reasoning_summary_text.done",
		"response.reasoning.done",
		"response.reasoning_text.done",
		"response.reasoning_summary.done":
		// Reasoning is delta-only on the Thinking channel; no close
		// signal needed.
	case "response.output_item.added":
		var ev sseOutputItemAdded
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, fmt.Errorf("sse output_item.added: %w", err))
		}
		if ev.Item.Type != sseTypeFunctionCall {
			return false
		}
		callID := ev.Item.CallID
		if callID == "" {
			// Some payloads only carry the item id; fall back so we
			// still have a stable handle to forward.
			callID = ev.Item.ID
		}
		pc := &pendingToolCall{id: callID, name: ev.Item.Name}
		// Some backends deliver the whole arguments object here rather than
		// via function_call_arguments.delta events; seed it so a delta-less
		// call isn't dispatched with empty args.
		pc.arguments.WriteString(ev.Item.Arguments)
		s.toolCallByIdx[ev.OutputIdx] = pc
		if ev.Item.ID != "" {
			s.toolCallByID[ev.Item.ID] = pc
		}
		// Emit the call name (and any args that arrived on this event)
		// immediately so downstream UIs can show "calling X..." before the
		// rest of the arguments stream in.
		if !yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{
			ID:   pc.id,
			Type: sseTypeFunction,
			Function: llm.ToolCallFunction{
				Name:      pc.name,
				Arguments: ev.Item.Arguments,
			},
		}}}, nil) {
			return true
		}
	case "response.function_call_arguments.delta":
		var ev sseFunctionArgsDelta
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, fmt.Errorf("sse function_call_arguments.delta: %w", err))
		}
		pc := s.pendingCall(ev.OutputIdx, ev.ItemID)
		if pc == nil {
			// Args before an added event — synthesize a placeholder.
			pc = &pendingToolCall{id: ev.ItemID}
			s.toolCallByIdx[ev.OutputIdx] = pc
			if ev.ItemID != "" {
				s.toolCallByID[ev.ItemID] = pc
			}
		}
		pc.arguments.WriteString(ev.Delta)
		if !yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{
			ID:   pc.id,
			Type: sseTypeFunction,
			Function: llm.ToolCallFunction{
				Arguments: ev.Delta,
			},
		}}}, nil) {
			return true
		}
	case "response.function_call_arguments.done":
		var ev sseFunctionArgsDone
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, fmt.Errorf("sse function_call_arguments.done: %w", err))
		}
		pc := s.pendingCall(ev.OutputIdx, ev.ItemID)
		if pc == nil {
			// done without a preceding added/delta — synthesize a placeholder
			// so the call's args still reach the consumer.
			pc = &pendingToolCall{id: ev.ItemID}
			s.toolCallByIdx[ev.OutputIdx] = pc
			if ev.ItemID != "" {
				s.toolCallByID[ev.ItemID] = pc
			}
		}
		// The done event carries the COMPLETE arguments; the consumer
		// (drain) accumulates by appending, so emit only the part not already
		// streamed via added/delta. Normally that's empty (deltas covered it);
		// for a delta-less call it's the whole object.
		remainder := unemittedSuffix(pc.arguments.String(), ev.Arguments)
		if remainder == "" {
			return false
		}
		pc.arguments.WriteString(remainder)
		if !yield(llm.CompletionChunk{ToolCalls: []llm.ToolCall{{
			ID:   pc.id,
			Type: sseTypeFunction,
			Function: llm.ToolCallFunction{
				Arguments: remainder,
			},
		}}}, nil) {
			return true
		}
	case "response.completed":
		var ev sseCompleted
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return !yield(llm.CompletionChunk{}, fmt.Errorf("sse completed: %w", err))
		}
		finish := "stop"
		if len(s.toolCallByIdx) > 0 {
			finish = "tool_calls"
		}
		yield(llm.CompletionChunk{
			Done:         true,
			FinishReason: finish,
			Usage: &llm.Usage{
				PromptTokens:     ev.Response.Usage.InputTokens,
				CompletionTokens: ev.Response.Usage.OutputTokens,
				TotalTokens:      ev.Response.Usage.TotalTokens,
				CachedTokens:     ev.Response.Usage.InputTokensDetails.CachedTokens,
			},
		}, nil)
		return true
	case "response.failed", "response.incomplete":
		var ev sseFailed
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			yield(llm.CompletionChunk{}, fmt.Errorf("sse failed: %w", err))
			return true
		}
		msg := ev.Response.Error.Message
		if msg == "" {
			msg = env.Type
		}
		yield(llm.CompletionChunk{
			Done:         true,
			FinishReason: "error",
		}, fmt.Errorf("codex response %s: %s", env.Type, msg))
		return true
	default:
		// Quietly ignore events we don't model (created, in_progress,
		// output_item.done, content_part.added, etc.). They're not
		// load-bearing for chunk emission.
	}
	return false
}

// flush emits a synthetic Done chunk when the stream ends without a
// terminal event (truncated response, connection drop) so downstream
// consumers move on. Reasoning lives on the out-of-band Thinking
// channel as discrete deltas, so there's no dangling block to close.
func (s *sseState) flush(yield func(llm.CompletionChunk, error) bool) {
	yield(llm.CompletionChunk{Done: true, FinishReason: "length"}, nil)
}

// unemittedSuffix returns the part of full that hasn't been emitted yet, given
// the args already streamed for this call. When the running buffer is a prefix
// of full (the normal case — deltas built up toward the complete object), it
// returns the trailing remainder; when full doesn't extend what was emitted
// (the deltas disagree with the done payload), it returns "" rather than risk
// re-emitting and corrupting the accumulated JSON.
func unemittedSuffix(emitted, full string) string {
	if emitted == "" {
		return full
	}
	if strings.HasPrefix(full, emitted) {
		return full[len(emitted):]
	}
	return ""
}

// pendingCall returns the in-flight tool call matched by output_index
// or item_id (preferring item_id when set, since output_index can be
// reused across non-tool items).
func (s *sseState) pendingCall(idx int, itemID string) *pendingToolCall {
	if itemID != "" {
		if pc, ok := s.toolCallByID[itemID]; ok {
			return pc
		}
	}
	if pc, ok := s.toolCallByIdx[idx]; ok {
		return pc
	}
	return nil
}
