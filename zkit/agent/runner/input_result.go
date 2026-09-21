package runner

import "github.com/zarldev/zarlmono/zkit/ai/tools"

// RetainingTruncator reports whether formatted output retains the full result
// or a host-generated recovery reference. The proof comes from the truncation
// owner, never from recognizing a path or receipt in model-visible text.
type RetainingTruncator interface {
	Truncator
	TruncateRetained(text, toolName string) (formatted string, retained bool)
}

func (r *Runner) inputResultText(result *tools.ToolResult, toolName string) (string, bool) {
	full := fullToolResultText(result)
	if truncator, ok := r.truncator.(RetainingTruncator); ok {
		return truncator.TruncateRetained(full, toolName)
	}
	formatted := r.truncator.Truncate(full, toolName)
	return formatted, formatted == full
}
