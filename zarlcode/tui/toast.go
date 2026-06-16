package tui

import (
	"strings"
)

func ensureToastPrefix(msg, prefix string) string {
	trimmed := strings.TrimSpace(msg)
	if strings.HasPrefix(trimmed, prefix) {
		return trimmed
	}
	return prefix + " " + trimmed
}

func inferToastTone(msg string) toastTone {
	s := strings.ToLower(strings.TrimSpace(msg))
	switch {
	case strings.HasPrefix(s, "✓"):
		return toastSuccess
	case strings.HasPrefix(s, "✗"):
		return toastError
	case isErrorStatus(s):
		return toastError
	default:
		return toastInfo
	}
}

// isErrorStatus is a light heuristic for whether a status reads as a failure,
// so footer toasts can use the error tone when a pane only gives us text.
func isErrorStatus(s string) bool {
	s = strings.ToLower(s)
	for _, k := range []string{
		"error", "failed", "unavailable", "can't", "want a", "nothing to", "no models", "doesn't",
		"required", "must be", "refused", "add:", "save key:", "set active:", "fetch models:", "delete:", "toggle:",
	} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func renderFooterToast(text string, tone toastTone) string {
	if text == "" {
		return ""
	}

	bg := palette.Highlight.BG()
	fg := toastForeground(tone)
	if fg == "" {
		fg = palette.Subtle.FG()
	}
	if fg == "" {
		fg = palette.Fg.FG()
	}

	open := "\x1b[7m" // reverse-video fallback when theme colours are unset.
	if bg != "" || fg != "" {
		open = bg + fg
	}
	return open + " " + text + " \x1b[0m"
}

func toastForeground(tone toastTone) string {
	switch tone {
	case toastSuccess:
		return palette.Success.FG()
	case toastError:
		return palette.Error.FG()
	default:
		return palette.Subtle.FG()
	}
}
