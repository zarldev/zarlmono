package claudecode

import (
	"math"
	"os"
	"strconv"
	"strings"
)

// ListPresetModels returns Claude Code model aliases / ids accepted by the
// Claude Code CLI. Keep aliases first because they track Anthropic's current
// recommended model without requiring zarlcode releases for every model bump.
func ListPresetModels() []string {
	return []string{
		"sonnet",
		"opus",
		"fable",
		"claude-sonnet-5-5",
		"claude-opus-5-5",
		"claude-haiku-5-5",
	}
}

// ContextWindowFor resolves a conservative CLI compaction window, not an API
// account entitlement. Opaque aliases keep the 200k fallback; explicit model
// pins and [1m] requests select documented windows. Inherited CLI caps apply.
// See https://code.claude.com/docs/en/model-config.
func ContextWindowFor(model string) int {
	if model == "" {
		return 0
	}
	model = strings.ToLower(strings.TrimSpace(model))
	extended := strings.HasSuffix(model, "[1m]")
	alias := strings.TrimSuffix(model, "[1m]")
	switch alias {
	case "sonnet", "opus", "haiku", "fable":
		if pin := strings.TrimSpace(os.Getenv("ANTHROPIC_DEFAULT_" + strings.ToUpper(alias) + "_MODEL")); pin != "" {
			model = strings.ToLower(pin)
		}
	}
	window := 200_000
	if extended || strings.HasSuffix(model, "[1m]") {
		window = 1_000_000
	}
	switch model {
	case "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5", "claude-sonnet-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5", "claude-fable-5-1":
		window = 1_000_000
	}
	if disabled, _ := strconv.ParseBool(os.Getenv("CLAUDE_CODE_DISABLE_1M_CONTEXT")); disabled {
		window = min(window, 200_000)
	}
	if compactWindow := cliAutoCompactWindow(os.Getenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW")); compactWindow > 0 {
		window = min(window, compactWindow)
	}
	return window
}

// cliAutoCompactWindow matches the current Claude Code CLI >=2.1.304 numeric
// and k/m suffix semantics. Older CLI versions may interpret this value differently.
func cliAutoCompactWindow(raw string) int {
	token := strings.TrimSpace(raw)
	if token == "" {
		return 0
	}

	multiplier := float64(1)
	switch strings.ToLower(token[len(token)-1:]) {
	case "k":
		multiplier = 1_000
		token = strings.TrimSpace(token[:len(token)-1])
	case "m":
		multiplier = 1_000_000
		token = strings.TrimSpace(token[:len(token)-1])
	}

	value, err := strconv.ParseFloat(token, 64)
	if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	value *= multiplier
	if value < 100_000 {
		return 100_000
	}
	if value > 1_000_000 {
		return 1_000_000
	}
	return int(value)
}
