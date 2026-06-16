package openaicodex

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// Capabilities reports what a Codex (ChatGPT-account) model supports. The
// GPT-5 codex line streams, calls tools, takes a system prompt, accepts
// images, and reasons. Cost is covered by the ChatGPT subscription, so it is
// not metered per token (the registry reports that separately).
func Capabilities(string) llm.ModelCapabilities {
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision:    true,
		SupportsThinking:  true,
	}
}
