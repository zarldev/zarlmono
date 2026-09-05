package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	programtools "github.com/zarldev/zarlmono/zkit/agent/tools/program"
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
	if strings.EqualFold(name, "program") {
		return programArgHint(params)
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
	case "skill_load":
		return get("name")
	case "agent_spawn", "agent", "task":
		prompt := get("prompt", "task", "goal", "objective")
		if agent := get("agent"); agent != "" {
			if prompt != "" {
				return agent + ": " + prompt
			}
			return agent
		}
		return prompt
	case "computer_act":
		return computerActionHint(params)
	case "computer_observe":
		return computerObserveHint(params)
	default:
		return ""
	}
}

func computerActionHint(params map[string]any) string {
	action, _ := params["action"].(map[string]any)
	kind, _ := action["kind"].(string)
	if kind == "" {
		return ""
	}

	switch kind {
	case "navigate":
		return strings.TrimSpace(kind + " " + stringArg(action, "url"))
	case "press":
		return strings.TrimSpace(kind + " " + stringArg(action, "key"))
	case "scroll":
		if delta, ok := action["delta"].(map[string]any); ok {
			return fmt.Sprintf("%s %v,%v", kind, delta["x"], delta["y"])
		}
	}
	return strings.TrimSpace(kind + " " + computerTargetHint(action["target"]))
}

func computerObserveHint(params map[string]any) string {
	fields := make([]string, 0, 4)
	for _, field := range []struct {
		key   string
		label string
	}{
		{"include_screenshot", "screenshot"},
		{"include_targets", "targets"},
		{"include_text", "text"},
		{"include_raw", "raw"},
	} {
		if included, _ := params[field.key].(bool); included {
			fields = append(fields, field.label)
		}
	}
	return strings.Join(fields, ", ")
}

func computerTargetHint(value any) string {
	target, _ := value.(map[string]any)
	for _, key := range []string{"id", "name", "text", "locator"} {
		if hint := stringArg(target, key); hint != "" {
			return hint
		}
	}
	if position, ok := target["position"].(map[string]any); ok {
		return fmt.Sprintf("%v,%v", position["x"], position["y"])
	}
	return ""
}

func stringArg(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func programArgHint(params map[string]any) string {
	script, ok := params["script"].(string)
	if !ok || strings.TrimSpace(script) == "" {
		return ""
	}
	inspect, err := programtools.Inspect(script)
	if err == nil && len(inspect.Calls) > 0 {
		return programInspectionHint(inspect)
	}
	if inspect.Dynamic {
		return "dynamic script"
	}
	calls := strings.Count(script, "call(") + strings.Count(script, "call_many(")
	if calls == 0 {
		return "script"
	}
	return fmt.Sprintf("%d call(s)", calls)
}

func programInspectionHint(inspect programtools.Inspection) string {
	parts := make([]string, 0, min(len(inspect.Calls), 3))
	for i, call := range inspect.Calls {
		if i >= 3 {
			parts = append(parts, fmt.Sprintf("+%d", len(inspect.Calls)-i))
			break
		}
		tool := call.Name.String()
		if tool == "" {
			tool = "dynamic"
		}
		if hint := rawToolArg(tool, map[string]any(call.Args)); hint != "" && len(inspect.Calls) == 1 && !call.Dynamic {
			parts = append(parts, tool+"  "+firstLine(hint))
			continue
		}
		if call.Dynamic {
			tool += "?"
		}
		parts = append(parts, tool)
	}
	if inspect.Dynamic {
		parts = append(parts, "dynamic")
	}
	return strings.Join(parts, ", ")
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}
