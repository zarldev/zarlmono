package tui

import "encoding/json"

// toolViewController is carried through nested disclosure wrappers so the
// selected call, not its container, owns its independent presentation state.
type toolViewController interface{ applyToolViewKey(string) bool }

type toolDrawerPrefix struct {
	lines                            []string
	argumentsRow, resultRow, viewRow int // item-local lines; -1 means absent
}

func (t *toolItem) resultJSON() (string, bool) {
	if t.name == "bash" || t.name == "read" {
		return "", false
	}
	if _, images := t.data.(toolResultPresentation); images {
		return "", false
	}
	text := t.result
	if t.data != nil {
		data, err := json.Marshal(t.data)
		if err != nil {
			return "", false
		}
		text = string(data)
	}
	value, ok := readableJSONValue(text)
	if !ok {
		return "", false
	}
	switch value.(type) {
	case map[string]any, []any:
		_, safe := presentToolCall("", nil, text)
		return safe, true
	default:
		return "", false
	}
}

func (t *toolItem) drawerPrefix(width int) toolDrawerPrefix {
	prefix := toolDrawerPrefix{argumentsRow: -1, resultRow: -1, viewRow: -1}
	if !t.expanded {
		return prefix
	}
	bodyWidth := max(1, width-t.depth*2)
	_, jsonResult := t.resultJSON()
	if t.input != "" || jsonResult {
		label := "view: readable"
		if t.jsonView {
			label = "view: json"
		}
		prefix.viewRow = 1 + len(prefix.lines)
		prefix.lines = append(prefix.lines, renderToolDrawerHeader(bodyWidth, label)...)
	}
	if t.input != "" {
		prefix.argumentsRow = 1 + len(prefix.lines)
		label := "[-] arguments:"
		if t.argumentsCollapsed {
			label = "[+] arguments:"
		}
		prefix.lines = append(prefix.lines, renderToolDrawerHeader(bodyWidth, label)...)
		if !t.argumentsCollapsed {
			if t.jsonView {
				_, safe := presentToolCall(t.name, nil, t.input)
				prefix.lines = append(prefix.lines, renderPlain(bodyWidth, safe, withBodyPrefix("    "))...)
			} else {
				prefix.lines = append(prefix.lines, prefixLines(readableToolInputLines(max(1, bodyWidth-4), t.name, t.input), "    ")...)
			}
		}
	}
	if t.hasResult() && !t.suppressesResultBody() {
		prefix.resultRow = 1 + len(prefix.lines)
		label := "[-] " + t.resultSectionTitle()
		if t.resultCollapsed {
			label = "[+] " + t.resultSectionTitle()
		}
		prefix.lines = append(prefix.lines, renderToolDrawerHeader(bodyWidth, label)...)
	}
	return prefix
}

func renderToolDrawerHeader(width int, label string) []string {
	return renderPlain(width, label, withBodyPrefix(childIndent), withStyle(palette.Subtle.On))
}

func (t *toolItem) applyToolViewKey(key string) bool {
	switch key {
	case "a":
		if t.input == "" {
			return false
		}
		if !t.expanded {
			t.argumentsCollapsed = false
		} else {
			t.argumentsCollapsed = !t.argumentsCollapsed
		}
	case "r":
		if !t.hasResult() || t.suppressesResultBody() {
			return false
		}
		if !t.expanded {
			t.resultCollapsed = false
		} else {
			t.resultCollapsed = !t.resultCollapsed
		}
	case "j":
		_, jsonResult := t.resultJSON()
		if t.input == "" && !jsonResult {
			return false
		}
		t.jsonView = !t.jsonView
	default:
		return false
	}
	t.expanded = true
	t.bump()
	return true
}
