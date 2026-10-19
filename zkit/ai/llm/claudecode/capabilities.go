package claudecode

import "github.com/zarldev/zarlmono/zkit/ai/llm"

// Capabilities reports what a Claude Code (CLI/subscription) model supports.
// The aliases target current Claude models, which stream, call tools, take a
// system prompt, and reason. This text-only CLI adapter does not transmit
// images. Cost is covered by the Claude
// subscription, so it is not metered per token.
func Capabilities(string) llm.ModelCapabilities {
	return llm.ModelCapabilities{
		SupportsStreaming: true,
		SupportsTools:     true,
		SupportsSystem:    true,
		SupportsVision:    false,
		SupportsThinking:  true,
	}
}
