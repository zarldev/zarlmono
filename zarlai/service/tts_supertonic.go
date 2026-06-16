package service

import (
	"context"
	"fmt"
	"strings"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// SupertonicSynthesizer uses sherpa-onnx with a Supertonic TTS bundle.
// Mirrors KokoroSynthesizer's surface so VoiceController can hold either
// behind the same handful of methods.
type SupertonicSynthesizer struct {
	mu  sync.Mutex
	tts *sherpa.OfflineTts
	sid int
	spd float32
}

// SupertonicConfig holds paths to a Supertonic ONNX bundle. The seven
// files below match the layout sherpa-onnx ships under
// sherpa-onnx-supertonic-tts-int8-*.
type SupertonicConfig struct {
	DurationPredictor string
	TextEncoder       string
	VectorEstimator   string
	Vocoder           string
	TtsJson           string
	UnicodeIndexer    string
	VoiceStyle        string
	Speed             float32
	Speaker           int
}

func NewSupertonicSynthesizer(cfg SupertonicConfig) (*SupertonicSynthesizer, error) {
	if cfg.Speed == 0 {
		cfg.Speed = 1.0
	}

	config := &sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Supertonic: sherpa.OfflineTtsSupertonicModelConfig{
				DurationPredictor: cfg.DurationPredictor,
				TextEncoder:       cfg.TextEncoder,
				VectorEstimator:   cfg.VectorEstimator,
				Vocoder:           cfg.Vocoder,
				TtsJson:           cfg.TtsJson,
				UnicodeIndexer:    cfg.UnicodeIndexer,
				VoiceStyle:        cfg.VoiceStyle,
			},
			NumThreads: 4,
			Provider:   "cpu",
		},
	}

	tts := sherpa.NewOfflineTts(config)
	if tts == nil {
		return nil, fmt.Errorf("create supertonic tts: model init failed")
	}

	return &SupertonicSynthesizer{
		tts: tts,
		sid: cfg.Speaker,
		spd: cfg.Speed,
	}, nil
}

func (s *SupertonicSynthesizer) Synthesize(_ context.Context, text string) ([]int16, error) {
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

func (s *SupertonicSynthesizer) SampleRate() int { return s.tts.SampleRate() }

func (s *SupertonicSynthesizer) Tune(speaker int, speed float32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sid = speaker
	if speed > 0 {
		s.spd = speed
	}
}

func (s *SupertonicSynthesizer) Speaker() int     { return s.sid }
func (s *SupertonicSynthesizer) Speed() float32   { return s.spd }
func (s *SupertonicSynthesizer) NumSpeakers() int { return s.tts.NumSpeakers() }

func (s *SupertonicSynthesizer) Close() {
	if s.tts != nil {
		sherpa.DeleteOfflineTts(s.tts)
	}
}
