package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// Transcriber converts audio to text.
type Transcriber interface {
	Transcribe(ctx context.Context, wav []byte) (string, error)
	Close()
}

var nonSpeechAnnotationRe = regexp.MustCompile(`[\[(][^\])]*[\])]`)

// IsNonSpeech reports whether text is empty or contains only Whisper-style
// non-speech annotations like "[Music]", "(upbeat music)", "[BLANK_AUDIO]".
// Whisper hallucinates these when the mic picks up music or ambient noise;
// callers should drop the segment instead of treating it as a user turn.
func IsNonSpeech(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return true
	}
	return strings.TrimSpace(nonSpeechAnnotationRe.ReplaceAllString(s, "")) == ""
}

// MoonshineTranscriber uses sherpa-onnx with Moonshine models.
type MoonshineTranscriber struct {
	mu         sync.Mutex
	recognizer *sherpa.OfflineRecognizer
	config     *sherpa.OfflineRecognizerConfig
}

// MoonshineConfig holds paths to Moonshine model files.
type MoonshineConfig struct {
	Preprocess     string
	Encoder        string
	UncachedDecode string
	CachedDecode   string
	Tokens         string
	NumThreads     int
}

func NewMoonshineTranscriber(cfg MoonshineConfig) (*MoonshineTranscriber, error) {
	if cfg.NumThreads == 0 {
		cfg.NumThreads = 4
	}

	config := &sherpa.OfflineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: 16000,
			FeatureDim: 80,
		},
		ModelConfig: sherpa.OfflineModelConfig{
			Moonshine: sherpa.OfflineMoonshineModelConfig{
				Preprocessor:    cfg.Preprocess,
				Encoder:         cfg.Encoder,
				UncachedDecoder: cfg.UncachedDecode,
				CachedDecoder:   cfg.CachedDecode,
			},
			Tokens:     cfg.Tokens,
			NumThreads: cfg.NumThreads,
			Provider:   "cpu",
		},
		DecodingMethod: "greedy_search",
	}

	recognizer := sherpa.NewOfflineRecognizer(config)
	if recognizer == nil {
		return nil, fmt.Errorf("create recognizer: model init failed")
	}

	return &MoonshineTranscriber{recognizer: recognizer, config: config}, nil
}

func (t *MoonshineTranscriber) recreate() {
	if t.recognizer != nil {
		sherpa.DeleteOfflineRecognizer(t.recognizer)
		t.recognizer = nil
	}
	r := sherpa.NewOfflineRecognizer(t.config)
	if r == nil {
		slog.Warn("sherpa-onnx recognizer recreate returned nil")
	}
	t.recognizer = r
}

func (t *MoonshineTranscriber) Transcribe(_ context.Context, wav []byte) (string, error) {
	samples, sampleRate, err := decodeWAV(wav)
	if err != nil {
		return "", fmt.Errorf("decode wav: %w", err)
	}

	if len(samples) == 0 {
		return "", nil
	}

	// sherpa-onnx OfflineRecognizer corrupts after repeated use —
	// recreate it every call. The tiny model loads in ~10ms.
	t.mu.Lock()
	defer t.mu.Unlock()

	// Limit audio to 30 seconds
	maxSamples := sampleRate * 30
	if len(samples) > maxSamples {
		samples = samples[:maxSamples]
	}

	t.recreate()

	stream := sherpa.NewOfflineStream(t.recognizer)
	if stream == nil {
		return "", fmt.Errorf("create stream: nil")
	}
	defer sherpa.DeleteOfflineStream(stream)

	stream.AcceptWaveform(sampleRate, samples)
	t.recognizer.Decode(stream)

	result := stream.GetResult()
	if result == nil {
		slog.Warn("sherpa-onnx GetResult returned nil", "samples", len(samples))
		return "", nil
	}
	return result.Text, nil
}

func (t *MoonshineTranscriber) Close() {
	if t.recognizer != nil {
		sherpa.DeleteOfflineRecognizer(t.recognizer)
	}
}

// WhisperTranscriber uses sherpa-onnx with Whisper models.
type WhisperTranscriber struct {
	mu         sync.Mutex
	recognizer *sherpa.OfflineRecognizer
	config     *sherpa.OfflineRecognizerConfig
}

// WhisperConfig holds paths to Whisper model files.
type WhisperConfig struct {
	Encoder    string
	Decoder    string
	Tokens     string
	Language   string
	NumThreads int
}

func NewWhisperTranscriber(cfg WhisperConfig) (*WhisperTranscriber, error) {
	if cfg.NumThreads == 0 {
		cfg.NumThreads = 4
	}
	if cfg.Language == "" {
		cfg.Language = "en"
	}

	config := &sherpa.OfflineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: 16000,
			FeatureDim: 80,
		},
		ModelConfig: sherpa.OfflineModelConfig{
			Whisper: sherpa.OfflineWhisperModelConfig{
				Encoder:  cfg.Encoder,
				Decoder:  cfg.Decoder,
				Language: cfg.Language,
				Task:     "transcribe",
			},
			Tokens:     cfg.Tokens,
			NumThreads: cfg.NumThreads,
			Provider:   "cpu",
		},
		DecodingMethod: "greedy_search",
	}

	recognizer := sherpa.NewOfflineRecognizer(config)
	if recognizer == nil {
		return nil, fmt.Errorf("create recognizer: model init failed")
	}

	return &WhisperTranscriber{recognizer: recognizer, config: config}, nil
}

func (t *WhisperTranscriber) Transcribe(_ context.Context, wav []byte) (string, error) {
	samples, sampleRate, err := decodeWAV(wav)
	if err != nil {
		return "", fmt.Errorf("decode wav: %w", err)
	}

	if len(samples) == 0 {
		return "", nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	maxSamples := sampleRate * 30
	if len(samples) > maxSamples {
		samples = samples[:maxSamples]
	}

	stream := sherpa.NewOfflineStream(t.recognizer)
	if stream == nil {
		return "", fmt.Errorf("create stream: nil")
	}
	defer sherpa.DeleteOfflineStream(stream)

	stream.AcceptWaveform(sampleRate, samples)
	t.recognizer.Decode(stream)

	result := stream.GetResult()
	if result == nil {
		slog.Warn("whisper GetResult returned nil", "samples", len(samples))
		return "", nil
	}
	return result.Text, nil
}

func (t *WhisperTranscriber) Close() {
	if t.recognizer != nil {
		sherpa.DeleteOfflineRecognizer(t.recognizer)
	}
}

// decodeWAV extracts float32 samples from a WAV file.
func decodeWAV(data []byte) ([]float32, int, error) {
	r := bytes.NewReader(data)

	// RIFF header
	var riff [4]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return nil, 0, fmt.Errorf("read riff: %w", err)
	}
	if string(riff[:]) != "RIFF" {
		return nil, 0, fmt.Errorf("not a WAV file")
	}

	var fileSize uint32
	binary.Read(r, binary.LittleEndian, &fileSize)

	var wave [4]byte
	io.ReadFull(r, wave[:])
	if string(wave[:]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a WAV file")
	}

	// Find fmt chunk
	var sampleRate uint32
	var bitsPerSample uint16
	var numChannels uint16

	for {
		var chunkID [4]byte
		var chunkSize uint32
		if _, err := io.ReadFull(r, chunkID[:]); err != nil {
			return nil, 0, fmt.Errorf("read chunk: %w", err)
		}
		binary.Read(r, binary.LittleEndian, &chunkSize)

		switch string(chunkID[:]) {
		case "fmt ":
			var audioFormat uint16
			binary.Read(r, binary.LittleEndian, &audioFormat)
			binary.Read(r, binary.LittleEndian, &numChannels)
			binary.Read(r, binary.LittleEndian, &sampleRate)
			// Skip byte rate and block align
			_, _ = r.Seek(4+2, io.SeekCurrent)
			binary.Read(r, binary.LittleEndian, &bitsPerSample)
			// Skip any extra fmt bytes
			remaining := int64(chunkSize) - 16
			if remaining > 0 {
				_, _ = r.Seek(remaining, io.SeekCurrent)
			}
		case "data":
			if bitsPerSample != 16 {
				return nil, 0, fmt.Errorf("unsupported bits per sample: %d", bitsPerSample)
			}
			numSamples := int(chunkSize) / int(numChannels) / 2
			samples := make([]float32, numSamples)
			for i := range numSamples {
				var sample int16
				binary.Read(r, binary.LittleEndian, &sample)
				samples[i] = float32(sample) / 32768.0
				// Skip extra channels
				for c := uint16(1); c < numChannels; c++ {
					var skip int16
					binary.Read(r, binary.LittleEndian, &skip)
				}
			}
			return samples, int(sampleRate), nil
		default:
			_, _ = r.Seek(int64(chunkSize), io.SeekCurrent)
		}
	}
}
