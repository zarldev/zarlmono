package service

import (
	"regexp"
	"strings"
)

// thinkingFormats is the set of wire-format patterns we know about for
// inline reasoning. Each entry captures the reasoning text in group 1.
//
//	xmlTag    — <think>…</think>, <thinking>…</thinking>, <reasoning>…
//	            </reasoning> (qwen3, deepseek, and most open models)
//	gemma4    — <|channel>thought … <channel|> (unsloth/Gemma-4 explicit
//	            thinking mode; note the close tag's reversed pipe position)
//
// Adding a new format is a one-line regex; no caller above the client
// layer should ever match these patterns again.
var thinkingFormats = []*regexp.Regexp{
	// XML-style tags (qwen3, deepseek, ...).
	regexp.MustCompile(`(?is)<(?:think|thinking|reasoning)>([\s\S]*?)</(?:think|thinking|reasoning)>`),
	// Gemma-4 channel format: <|channel>thought … <channel|>.
	// The close marker has the pipe before `>` — literal, not a typo.
	regexp.MustCompile(`(?is)<\|channel>thought([\s\S]*?)<channel\|>`),
}

// SplitThinking separates inline reasoning from a model response.
// Returns (cleanContent, joinedThinking). Intended for provider clients
// whose wire format has no dedicated thinking channel (Ollama, llama.cpp,
// OpenAI-compatible). Anthropic exposes thinking as a distinct content
// block, so its client should skip this helper and use the native fields.
//
// Extraction runs every known format in sequence — the ranges don't
// overlap since each format's delimiters are disjoint, so order doesn't
// matter. Empty inner blocks (e.g. Gemma-4 emits an empty thought channel
// when thinking is disabled) are dropped from the joined result.
func SplitThinking(text string) (content, thinking string) {
	var thoughts []string
	for _, re := range thinkingFormats {
		text, thoughts = extractThinking(text, re, thoughts)
	}
	return strings.TrimSpace(text), strings.Join(thoughts, "\n\n")
}

func extractThinking(text string, re *regexp.Regexp, thoughts []string) (string, []string) {
	matches := re.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, thoughts
	}
	var buf strings.Builder
	cursor := 0
	for _, m := range matches {
		buf.WriteString(text[cursor:m[0]])
		// Submatch group 1 is the reasoning text between the delimiters.
		if m[2] != -1 && m[3] != -1 {
			if trimmed := strings.TrimSpace(text[m[2]:m[3]]); trimmed != "" {
				thoughts = append(thoughts, trimmed)
			}
		}
		cursor = m[1]
	}
	buf.WriteString(text[cursor:])
	return buf.String(), thoughts
}
