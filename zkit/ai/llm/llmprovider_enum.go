package llm

//go:generate go tool goenums -f llmprovider_enum.go

// llmProvider is the goenums source for LLMProvider — the canonical
// identity of every supported backend. The trailing comment on each
// constant is the stable wire/config name (what appears in LLM_PROVIDER,
// persisted provider rows, and each Provider.Name()); it is the single
// source of truth, so callers reference llm.LLMProviders.X rather than
// open-coding the string. goenums (>= v0.4.6) capitalises the leading
// "llm" initialism, so the generated type is LLMProvider, not LlmProvider.
type llmProvider int

const (
	openAI      llmProvider = iota // openai
	deepSeek                       // deepseek
	openAICodex                    // openai-codex
	google                         // google
	anthropic                      // anthropic
	claudeCode                     // claude-code
	llamaCpp                       // llamacpp
	ollama                         // ollama
)
