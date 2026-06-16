package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

var sentenceSplitRE = regexp.MustCompile(`([.!?])\s+`)

// Synthesizer converts text to speech audio. The chat path consumes only
// these methods; voice configuration (speaker / speed / engine selection)
// is the admin path's concern and lives on richer concrete types.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) ([]int16, error)
	SampleRate() int
	Close()
}

// EngineName identifies a TTS engine bundled into the binary.
type EngineName string

const (
	EngineKokoro     EngineName = "kokoro"
	EngineSupertonic EngineName = "supertonic"
)

// KokoroSynthesizer uses sherpa-onnx with Kokoro TTS models.
type KokoroSynthesizer struct {
	mu  sync.Mutex
	tts *sherpa.OfflineTts
	sid int
	spd float32
}

// KokoroConfig holds paths to Kokoro model files.
type KokoroConfig struct {
	Model   string
	Voices  string
	Tokens  string
	DataDir string
	Speed   float32
	Speaker int
}

func NewKokoroSynthesizer(cfg KokoroConfig) (*KokoroSynthesizer, error) {
	if cfg.Speed == 0 {
		cfg.Speed = 1.1
	}

	config := &sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Kokoro: sherpa.OfflineTtsKokoroModelConfig{
				Model:       cfg.Model,
				Voices:      cfg.Voices,
				Tokens:      cfg.Tokens,
				DataDir:     cfg.DataDir,
				LengthScale: 1.0,
			},
			NumThreads: 4,
			Provider:   "cpu",
		},
	}

	tts := sherpa.NewOfflineTts(config)
	if tts == nil {
		return nil, fmt.Errorf("create tts: model init failed")
	}

	return &KokoroSynthesizer{
		tts: tts,
		sid: cfg.Speaker,
		spd: cfg.Speed,
	}, nil
}

func (s *KokoroSynthesizer) Synthesize(_ context.Context, text string) ([]int16, error) {
	if text == "" {
		return nil, nil
	}
	if strings.ContainsRune(text, 0) {
		return nil, fmt.Errorf("synthesize: text contains null byte")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	audio := s.tts.Generate(text, s.sid, s.spd)
	if audio == nil || len(audio.Samples) == 0 {
		return nil, nil
	}

	// Convert float32 [-1,1] to int16
	pcm := make([]int16, len(audio.Samples))
	for i, sample := range audio.Samples {
		if sample > 1 {
			sample = 1
		} else if sample < -1 {
			sample = -1
		}
		pcm[i] = int16(sample * 32767)
	}

	return pcm, nil
}

func (s *KokoroSynthesizer) SampleRate() int {
	return s.tts.SampleRate()
}

// Tune changes the speaker and speed at runtime — a runtime reconfiguration,
// not initial construction, so it takes named arguments rather than the
// options-struct pattern NewKokoroSynthesizer uses.
func (s *KokoroSynthesizer) Tune(speaker int, speed float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sid = speaker
	if speed > 0 {
		s.spd = speed
	}
}

// Speaker returns the current speaker ID.
func (s *KokoroSynthesizer) Speaker() int { return s.sid }

// Speed returns the current speed.
func (s *KokoroSynthesizer) Speed() float32 { return s.spd }

// NumSpeakers returns how many speakers the model supports.
func (s *KokoroSynthesizer) NumSpeakers() int { return s.tts.NumSpeakers() }

func (s *KokoroSynthesizer) Close() {
	if s.tts != nil {
		sherpa.DeleteOfflineTts(s.tts)
	}
}

// SplitCompleteSentences divides a buffer into fully-terminated sentences
// and a trailing remainder that might still be in progress. A sentence is
// "complete" when it ends in .!? followed by whitespace — matching the
// streaming LLM behaviour where tokens after the period arrive in a
// subsequent chunk.
//
// When flushFinal is true, any non-empty remainder is returned as a
// sentence (the stream is known to be ending). Used by the transport's
// TTS sink to start speaking sentences as they arrive instead of waiting
// for the full reply.
func SplitCompleteSentences(text string, flushFinal bool) (sentences []string, remainder string) {
	indices := sentenceSplitRE.FindAllStringIndex(text, -1)
	prev := 0
	for _, idx := range indices {
		end := idx[0] + 1
		s := strings.TrimSpace(text[prev:end])
		if s != "" {
			sentences = append(sentences, s)
		}
		prev = idx[1]
	}
	if prev < len(text) {
		tail := text[prev:]
		if flushFinal {
			tail = strings.TrimSpace(tail)
			if tail != "" {
				sentences = append(sentences, tail)
			}
		} else {
			remainder = tail
		}
	}
	return sentences, remainder
}

// SplitSentences splits text into sentences for streaming TTS.
func SplitSentences(text string) []string {
	// Go regexp doesn't support lookbehinds, so we match the punctuation
	// and reattach it to the preceding text.
	indices := sentenceSplitRE.FindAllStringIndex(text, -1)
	if len(indices) == 0 {
		if text != "" {
			return []string{text}
		}
		return nil
	}

	var result []string
	prev := 0
	for _, idx := range indices {
		// idx[0] is start of punctuation, idx[0]+1 is after it
		end := idx[0] + 1 // include the punctuation char
		s := strings.TrimSpace(text[prev:end])
		if s != "" {
			result = append(result, s)
		}
		prev = idx[1] // skip the whitespace after punctuation
	}
	// Remaining text after last match
	if prev < len(text) {
		s := strings.TrimSpace(text[prev:])
		if s != "" {
			result = append(result, s)
		}
	}

	if len(result) == 0 && text != "" {
		return []string{text}
	}
	return result
}
