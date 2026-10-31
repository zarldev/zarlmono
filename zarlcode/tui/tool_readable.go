package tui

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func readableJSONValue(text string) (any, bool) {
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.UseNumber()
	var value, extra any
	if decoder.Decode(&value) != nil || decoder.Decode(&extra) != io.EOF {
		return nil, false
	}
	return value, true
}

// readableScalar decodes display strings, then escapes controls again: JSON
// decoding must never turn a safely stored escape back into terminal authority.
func readableScalar(value any) string {
	switch value := value.(type) {
	case string:
		if value == "" {
			return "(empty text)"
		}
		return escapeToolInputControls(redactToolInputText(value))
	case nil:
		return "null"
	case json.Number:
		return value.String()
	default:
		return escapeToolInputControls(mustJSON(value))
	}
}

func readableObjectKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// readableFieldLines preserves every value, using bounded indentation rather
// than a transport-shaped JSON dump. Deep subtrees retain a compact fallback.
func readableFieldLines(width int, label string, value any, depth int) []string {
	return readableFieldLinesLimited(width, label, value, depth, 0)
}

func readableFieldLinesLimited(width int, label string, value any, depth, limit int) []string {
	indent := strings.Repeat("  ", depth)
	label = escapeToolInputControls(label)
	label = strings.ReplaceAll(strings.ReplaceAll(label, "\n", `\n`), "\t", `\t`)
	if depth >= 6 {
		return renderReadableFieldText(width, indent, label+": "+readableScalar(value), limit)
	}
	var lines []string
	switch value := value.(type) {
	case map[string]any:
		if len(value) == 0 {
			text := "(empty object)"
			if label != "" {
				text = label + ": " + text
			}
			return renderPlain(width, text, withBodyPrefix(indent))
		}
		if label != "" {
			lines = renderPlain(width, label+":", withBodyPrefix(indent))
		}
		for _, key := range readableObjectKeys(value) {
			if limit > 0 && len(lines) >= limit {
				break
			}
			remaining := 0
			if limit > 0 {
				remaining = limit - len(lines)
			}
			lines = append(lines, readableFieldLinesLimited(width, key, value[key], depth+1, remaining)...)
		}
	case []any:
		if len(value) == 0 {
			text := "(empty list)"
			if label != "" {
				text = label + ": " + text
			}
			return renderPlain(width, text, withBodyPrefix(indent))
		}
		if label != "" {
			lines = renderPlain(width, label+":", withBodyPrefix(indent))
		}
		for index, child := range value {
			if limit > 0 && len(lines) >= limit {
				break
			}
			remaining := 0
			if limit > 0 {
				remaining = limit - len(lines)
			}
			lines = append(lines, readableFieldLinesLimited(width, strconv.Itoa(index+1), child, depth+1, remaining)...)
		}
	default:
		text := readableScalar(value)
		if limit > 0 && len(text) > largeToolPreviewBytes {
			if label != "" {
				text = label + ": " + text
			}
			return renderReadableFieldText(width, indent, text, limit)
		}
		if label == "" {
			return renderPlain(width, text, withBodyPrefix(indent))
		}
		if strings.Contains(text, "\n") {
			lines = renderPlain(width, label+":", withBodyPrefix(indent))
			lines = append(lines, renderPlain(width, text, withBodyPrefix(indent+"  "))...)
		} else {
			lines = renderPlain(width, label+": "+text, withBodyPrefix(indent))
		}
	}
	return lines
}

func renderReadableFieldText(width int, indent, text string, limit int) []string {
	if limit > 0 && len(text) > largeToolPreviewBytes {
		return prefixLines(renderLargeToolPreview(max(1, width-len(indent)), text, limit), indent)
	}
	return renderPlain(width, text, withBodyPrefix(indent))
}

func requestedPlan(value any) (code.Plan, bool) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return code.Plan{}, false
	}
	plan := code.Plan{Steps: make([]code.PlanStep, 0, len(items))}
	for _, item := range items {
		step, ok := item.(map[string]any)
		if !ok || len(step) != 2 {
			return code.Plan{}, false
		}
		text, textOK := step["step"].(string)
		status, statusOK := step["status"].(string)
		if !textOK || text == "" || !statusOK {
			return code.Plan{}, false
		}
		parsed, err := code.ParseStepStatus(status)
		if err != nil {
			return code.Plan{}, false
		}
		plan.Steps = append(plan.Steps, code.PlanStep{Text: escapeToolInputControls(text), Status: parsed})
	}
	return plan, true
}

func readableToolInputLines(width int, name, text string) []string {
	value, ok := readableJSONValue(text)
	if !ok {
		return renderPlain(width, "[argument details unavailable]")
	}
	value = redactToolInputValue(value)
	object, objectOK := value.(map[string]any)
	if !objectOK {
		return readableFieldLines(width, "", value, 0)
	}
	var lines []string
	consumed := make(map[string]bool)
	if name == "update_plan" {
		plan, valid := requestedPlan(object["plan"])
		explanation, hasExplanation := object["explanation"]
		explanationText, explanationOK := explanation.(string)
		if valid && (!hasExplanation || explanationOK) {
			plan.Explanation = escapeToolInputControls(explanationText)
			lines = append(lines, readablePlanLines(width, plan)...)
			consumed["plan"], consumed["explanation"] = true, true
		}
	}
	for _, key := range []string{"command", "cmd", "script"} {
		if content, present := object[key]; present {
			lines = append(lines, readableFieldLines(width, key, content, 0)...)
			consumed[key] = true
		}
	}
	for _, key := range readableObjectKeys(object) {
		if !consumed[key] {
			lines = append(lines, readableFieldLines(width, key, object[key], 0)...)
		}
	}
	if len(lines) == 0 {
		return renderPlain(width, "(no arguments)")
	}
	return lines
}

func readableJSONResultLines(width int, text string) ([]string, bool) {
	return readableJSONResultPreview(width, text, 0)
}

func readableJSONResultPreview(width int, text string, limit int) ([]string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	value, ok := readableJSONValue(trimmed)
	if !ok {
		return nil, false
	}
	budget := 0
	if limit > 0 {
		budget = limit + 1 // Discover overflow without formatting the rest of the tree.
	}
	lines := readableFieldLinesLimited(width, "", redactToolInputValue(value), 0, budget)
	if limit > 0 && len(lines) > limit {
		lines = append(lines[:limit-1], palette.Muted.On("… additional result content not rendered"))
	}
	return lines, true
}

func readablePlanLines(width int, plan code.Plan) []string {
	safe := code.Plan{Explanation: escapeToolInputControls(redactToolInputText(plan.Explanation)), Steps: make([]code.PlanStep, len(plan.Steps))}
	for i, step := range plan.Steps {
		step.Text = escapeToolInputControls(redactToolInputText(step.Text))
		safe.Steps[i] = step
	}
	return planInlineLines(safe, width)
}
