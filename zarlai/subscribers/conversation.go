package subscribers

import (
	"strings"

	"github.com/zarldev/zarlmono/zarlai/events"
)

// serializeConversation collapses a raw message log into a single text
// block readable by any LLM. Sending the history as alternating
// user/assistant turns — which is how summarizer/extractor/reflector
// used to ask — trips llama-server's Jinja template into thinking the
// trailing assistant turn is a prefill / continuation request, which
// Qwen3's thinking mode rejects with a 400. Wrapping the entire
// exchange as one payload avoids that path and is portable across
// providers.
func serializeConversation(msgs []events.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		label := m.Role
		switch m.Role {
		case "user":
			label = "User"
		case "assistant":
			label = "Assistant"
		case "system":
			// System messages aren't part of the conversation transcript.
			continue
		}
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
