package openai

// ContextWindowFor returns the published context window for an
// OpenAI model id. Centralises per-model knowledge that used to
// live in backends.openAIContextWindow as a small switch statement
// — moving it here keeps OpenAI's model metadata next to the
// provider implementation and stops the upstream dispatcher from
// silently defaulting unknown ids to 128k.
//
// The fallback for unknown models is intentionally NOT 128k. A
// model id the registry doesn't know about may genuinely have a
// smaller window (gpt-3.5 — 16k, gpt-4 — 8k, gpt-4-32k — 32k); the
// historical "default 128k" code lied UP for those, which silently
// painted a too-generous gauge and let the compactor postpone
// compaction past the real limit. Returning 0 means "unknown" so
// the caller can fall back to a probe or surface the unknown.
//
// GPT-5.x is intentionally listed here instead of routed through
// openaicodex.ContextWindowFor. The regular OpenAI API and the
// ChatGPT-account Codex backend can advertise different usable caps;
// the Codex OAuth path uses /codex/models plus a conservative fallback.
func ContextWindowFor(model string) int {
	if model == "" {
		return 0
	}
	switch model {
	// --- GPT-5 family ---
	case "gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna":
		return 1_050_000
	case "gpt-5.5":
		return 1_000_000
	case "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.3-codex-spark", "gpt-5.2":
		return 400_000

	// --- GPT-4.1 family: 1M context (newest hosted line) ---
	case "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano":
		return 1_000_000

	// --- o1 reasoning family ---
	case "o1":
		return 200_000
	case "o1-mini", "o1-preview":
		return 128_000

	// --- o3 reasoning family ---
	case "o3", "o3-mini":
		return 200_000

	// --- GPT-4o / 4-turbo: 128k ---
	case "gpt-4o", "gpt-4o-mini", "gpt-4o-2024-05-13", "gpt-4o-2024-08-06",
		"gpt-4-turbo", "gpt-4-turbo-2024-04-09", "gpt-4-turbo-preview",
		"gpt-4-0125-preview", "gpt-4-1106-preview":
		return 128_000

	// --- GPT-4 (legacy): 8k / 32k variants. The default 128k
	// fallback the old switch used overstated these by 16× — a
	// real bug for anyone still pinned to the legacy lines.
	case "gpt-4", "gpt-4-0613":
		return 8_192
	case "gpt-4-32k", "gpt-4-32k-0613":
		return 32_768

	// --- GPT-3.5: 16k for current variants, 4k for the original ---
	case "gpt-3.5-turbo", "gpt-3.5-turbo-0125", "gpt-3.5-turbo-1106",
		"gpt-3.5-turbo-16k", "gpt-3.5-turbo-16k-0613":
		return 16_385
	case "gpt-3.5-turbo-0613", "gpt-3.5-turbo-instruct":
		return 4_096
	}
	return 0
}
