package openai

//go:generate go tool goenums -f litellm_chat_enum.go

// liteLLMThinkingKind is the tagged Chat thinking-block vocabulary.
type liteLLMThinkingKind int

const (
	liteLLMThinking         liteLLMThinkingKind = iota // thinking
	liteLLMRedactedThinking                            // redacted_thinking
)

// liteLLMCacheTTL is an explicit Anthropic-compatible cache lifetime.
type liteLLMCacheTTL int

const (
	liteLLMCacheDefault liteLLMCacheTTL = iota // default
	liteLLMCache5m                             // 5m
	liteLLMCache1h                             // 1h
)

// liteLLMCacheKind is the tagged cache-control vocabulary.
type liteLLMCacheKind int

const (
	liteLLMCacheEphemeral liteLLMCacheKind = iota // ephemeral
)
