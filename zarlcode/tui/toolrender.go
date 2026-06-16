package tui

import (
	"strings"
	"unicode/utf8"
)

// toolArgHint renders a compact, tool-specific argument summary for the
// collapsed tool row — the command for bash, the path for file tools,
// the pattern for search, etc. Empty when the tool isn't recognised or
// carries no useful argument; the inspector shows the full call.
func toolArgHint(name string, params map[string]any) string {
	hint := firstLine(rawToolArg(name, params))
	const maxHint = 80
	if utf8.RuneCountInString(hint) > maxHint {
		hint = string([]rune(hint)[:maxHint-1]) + "…"
	}
	return hint
}

func rawToolArg(name string, params map[string]any) string {
	get := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := params[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	switch strings.ToLower(name) {
	case "bash", "shell", "sh":
		return "$ " + get("command", "cmd", "script")
	case "read_file", "read", "view", "cat":
		return get("path", "file", "filename", "target_file")
	case "write", "write_file", "create_file":
		return get("path", "file", "filename")
	case "edit", "multiedit", "str_replace", "str_replace_editor":
		return get("path", "file", "filename")
	case "grep", "search", "ripgrep":
		return get("pattern", "query", "regex", "regexp")
	case "glob", "ls", "list", "list_dir":
		return get("pattern", "path", "glob", "dir")
	case "load_skill":
		return get("name")
	case "spawn_agent", "agent", "task":
		prompt := get("prompt", "task", "goal", "objective")
		if agent := get("agent"); agent != "" {
			if prompt != "" {
				return agent + ": " + prompt
			}
			return agent
		}
		return prompt
	default:
		return ""
	}
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}
