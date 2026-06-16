package main

import (
	"os"
	"path/filepath"
)

// Config captures every environment-derived knob that main() needs. Loaded
// once at startup so wiring code doesn't reach for os.Getenv ad-hoc.
type Config struct {
	Port string

	// EmbedURL / EmbedModel point at any OpenAI-compatible
	// /v1/embeddings endpoint (Ollama at :11434/v1, OpenAI proper,
	// vLLM, llama.cpp --embeddings, hosted gateways, …).
	EmbedURL   string
	EmbedModel string

	// ChatURL / ChatModel point at any OpenAI-compatible
	// /v1/chat/completions endpoint. The README's default is the
	// bundled llama-server compose service, but anything that speaks
	// the shape works.
	ChatURL   string
	ChatModel string

	// TaskChatURL / TaskChatModel let the taskrunner point at a
	// distinct chat endpoint (different box, different quant,
	// different model) without disturbing the conversation path.
	// Empty defaults to ChatURL / ChatModel — the consolidated
	// deployment.
	TaskChatURL   string
	TaskChatModel string

	STTDir        string
	KokoroDir     string
	SupertonicDir string
	DoltDSN       string
	FaceModelsDir string
}

// LoadConfig reads the environment, applying the defaults that have
// historically lived inline in main(). Pure — no IO beyond os.Getenv.
func LoadConfig() Config {
	chatURL := os.Getenv("CHAT_URL")
	chatModel := firstNonEmpty(os.Getenv("CHAT_MODEL"), "qwen3.6-35b-a3b")
	modelsDir := firstNonEmpty(os.Getenv("MODELS_DIR"), "./deploy/models")
	return Config{
		Port: firstNonEmpty(os.Getenv("PORT"), "8080"),

		EmbedURL:   firstNonEmpty(os.Getenv("EMBED_URL"), "http://localhost:11434/v1"),
		EmbedModel: firstNonEmpty(os.Getenv("EMBED_MODEL"), "nomic-embed-text"),

		ChatURL:   chatURL,
		ChatModel: chatModel,

		TaskChatURL:   firstNonEmpty(os.Getenv("TASK_CHAT_URL"), chatURL),
		TaskChatModel: firstNonEmpty(os.Getenv("TASK_CHAT_MODEL"), chatModel),

		// Subpaths under MODELS_DIR follow upstream tarball conventions
		// (sherpa-onnx, dlib). Operators keep one tree and unpack into it.
		STTDir:        filepath.Join(modelsDir, "whisper-small-en"),
		KokoroDir:     filepath.Join(modelsDir, "kokoro-en-v0_19"),
		SupertonicDir: filepath.Join(modelsDir, "supertonic"),
		FaceModelsDir: filepath.Join(modelsDir, "dlib"),
		DoltDSN:       firstNonEmpty(os.Getenv("DOLT_DSN"), "root:@tcp(localhost:3307)/zarl?parseTime=true"),
	}
}

func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}
