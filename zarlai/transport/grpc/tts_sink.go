package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/zarldev/zarlmono/zarlai/repository"
	"github.com/zarldev/zarlmono/zarlai/service"
	zarlv1 "github.com/zarldev/zarlmono/zarlai/transport/grpc/gen/zarl/v1"
)

// ttsSink streams sentences into the synthesizer as content becomes
// available from the LLM, rather than waiting for the whole reply before
// speaking a word. Each call to push appends a content fragment, scans
// the buffer for newly-complete sentences, and synthesizes any that have
// just terminated — so the first spoken word lands a sentence-time after
// the model emits the first period instead of a full-reply-time later.
//
// Not goroutine-safe. The transport drives it from a single goroutine
// that consumes the LLM delta channel; the audio stream.Send calls are
// synchronous per chunk so there's no interleaving to worry about.
type ttsSink struct {
	ctx          context.Context
	stream       *connect.ServerStream[zarlv1.ConverseResponse]
	synthesizer  service.Synthesizer
	buf          strings.Builder
	audioStarted bool
	chunkIdx     int32
	displayName  string
	spokenName   string
	applySubst   bool
	ttsStart     time.Time
}

// newTTSSink builds a sink bound to the current Converse stream. Returns
// nil when no synthesizer is configured — callers treat nil sinks as
// no-ops so the LLM-streaming path works identically whether TTS is
// enabled or not.
func newTTSSink(
	ctx context.Context,
	stream *connect.ServerStream[zarlv1.ConverseResponse],
	synth service.Synthesizer,
	settings *repository.SettingsRepo,
) *ttsSink {
	if synth == nil {
		return nil
	}
	t := &ttsSink{ctx: ctx, stream: stream, synthesizer: synth}
	if settings != nil {
		displayName, _ := settings.Get(ctx, "agent_name")
		spokenName, _ := settings.Get(ctx, "agent_spoken_name")
		t.displayName = displayName
		t.spokenName = spokenName
		t.applySubst = displayName != "" && spokenName != "" && displayName != spokenName
	}
	return t
}

// push appends chunk to the buffer and speaks any sentences that just
// completed. Safe to call with an empty chunk (no-op) and with a nil
// receiver (no-op) so callers don't need to nil-check.
func (t *ttsSink) push(chunk string) error {
	if t == nil || chunk == "" {
		return nil
	}
	t.buf.WriteString(chunk)
	return t.flush(false)
}

// close flushes any remaining partial sentence and emits AudioEnd. Safe
// to call on a nil receiver or when nothing was ever pushed — no audio
// events fire in either case.
func (t *ttsSink) close() error {
	if t == nil {
		return nil
	}
	if err := t.flush(true); err != nil {
		return err
	}
	if !t.audioStarted {
		return nil
	}
	ttsTime := time.Since(t.ttsStart).Seconds()
	slog.Info("tts", "duration_sec", fmt.Sprintf("%.2f", ttsTime), "sentences", t.chunkIdx)
	return t.stream.Send(&zarlv1.ConverseResponse{
		Payload: &zarlv1.ConverseResponse_AudioEnd{
			AudioEnd: &zarlv1.AudioEnd{DurationSec: float32(ttsTime)},
		},
	})
}

func (t *ttsSink) flush(final bool) error {
	// Strip <tool_call> markup before sentence-splitting so the
	// synthesizer never receives raw tool-call JSON. Markup may span
	// chunks (one chunk ends with "<tool_call>{", the next carries the
	// rest): unclosed blocks are held back in the buffer so the next
	// flush can reassemble them. On a final flush an unclosed block is
	// discarded — ParseToolCallsFromText recovers the call from the
	// aggregate content at the transport layer.
	safe, held := stripToolCallMarkup(t.buf.String(), final)
	t.buf.Reset()
	if held != "" {
		t.buf.WriteString(held)
	}

	sentences, remainder := service.SplitCompleteSentences(safe, final)
	if remainder != "" {
		// Remainder is a partial sentence that's temporally BEFORE any
		// held tool-call opener — prepend so order is preserved.
		tail := t.buf.String()
		t.buf.Reset()
		t.buf.WriteString(remainder)
		t.buf.WriteString(tail)
	}
	for _, s := range sentences {
		if err := t.speak(s); err != nil {
			return err
		}
	}
	return nil
}

// stripToolCallMarkup removes <tool_call>...</tool_call> blocks from s.
// Returns the safe-to-speak prefix and any unclosed trailing block that
// must be held until more content arrives. When final is true, an
// unclosed block at the end is discarded — it's already safely captured
// in the aggregate content that chatOrStream returns, and the transport
// layer's ParseToolCallsFromText will decode it into real tool calls.
//
// Only the <tool_call> dialect is handled here — that's what Qwen3
// emits when the chat template's native tool-call wiring misfires.
// Gemma-4 and fenced-JSON dialects go through different response
// parsers; they'd need their own gating if they start appearing in
// practice.
func stripToolCallMarkup(s string, final bool) (safe, held string) {
	const openTag = "<tool_call>"
	const closeTag = "</tool_call>"
	var out strings.Builder
	for {
		open := strings.Index(s, openTag)
		if open < 0 {
			out.WriteString(s)
			return out.String(), ""
		}
		out.WriteString(s[:open])
		rest := s[open+len(openTag):]
		_, after, ok := strings.Cut(rest, closeTag)
		if !ok {
			if final {
				return out.String(), ""
			}
			return out.String(), s[open:]
		}
		s = after
	}
}

func (t *ttsSink) speak(sentence string) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	clean := stripMarkdown(sentence)
	if clean == "" {
		return nil
	}

	if !t.audioStarted {
		t.ttsStart = time.Now()
		// SentenceCount=-1 signals "unknown, streaming". Frontend treats
		// AudioChunk indices as informational; no count-based UI exists.
		if err := t.stream.Send(&zarlv1.ConverseResponse{
			Payload: &zarlv1.ConverseResponse_AudioStart{
				AudioStart: &zarlv1.AudioStart{
					SampleRate:    int32(t.synthesizer.SampleRate()),
					SentenceCount: -1,
				},
			},
		}); err != nil {
			return fmt.Errorf("send audio_start: %w", err)
		}
		t.audioStarted = true
	}

	ttsText := clean
	if t.applySubst {
		ttsText = strings.ReplaceAll(clean, t.displayName, t.spokenName)
	}
	pcm, err := t.synthesizer.Synthesize(t.ctx, ttsText)
	if err != nil {
		slog.Error("tts", "error", err, "sentence", clean)
		return nil
	}
	if len(pcm) == 0 {
		return nil
	}

	pcmBytes := make([]byte, len(pcm)*2)
	for j, sample := range pcm {
		pcmBytes[j*2] = byte(sample)
		pcmBytes[j*2+1] = byte(sample >> 8)
	}
	if err := t.stream.Send(&zarlv1.ConverseResponse{
		Payload: &zarlv1.ConverseResponse_AudioChunk{
			AudioChunk: &zarlv1.AudioChunk{Pcm: pcmBytes, Index: t.chunkIdx},
		},
	}); err != nil {
		return fmt.Errorf("send audio_chunk: %w", err)
	}
	t.chunkIdx++
	return nil
}
