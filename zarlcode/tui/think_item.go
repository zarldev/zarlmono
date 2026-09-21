package tui

import "strings"

// thinkingItem is the reasoning block for an assistant turn. Reasoning
// deltas arrive on the runner's out-of-band Thinking channel
// (appendThinking) and accumulate here across iterations — one block
// per turn. Collapsed by default ([+] thinking); browse + enter
// expands.
type thinkingItem struct {
	versioned
	depth       int
	nested      bool // turn activity: rendered tight (no blank line above)
	text        string
	expanded    bool
	done        bool
	interrupted bool
	children    []item // mode disclosures owned by this thinking block
	layout      childBlockCache
}

func (t *thinkingItem) finished() bool { return t.done }

func (t *thinkingItem) toggle() {
	t.expanded = !t.expanded
	t.bump()
}

// togglerAt shares child geometry with rendering and keyboard navigation.
func (t *thinkingItem) togglerAt(width, ln int) toggler {
	if ln == 0 {
		return t
	}
	if !t.expanded || len(t.children) == 0 {
		return nil
	}
	childWidth := width - 4
	bodyLines := len(t.bodyLines(width))
	return t.layout.render(t.children, childWidth, t.version()).togglerForLine(ln-bodyLines, childWidth, t.children, t.bump)
}

func (t *thinkingItem) toggleLocals(width int) []int {
	if !t.expanded || len(t.children) == 0 {
		return []int{0}
	}
	childWidth := width - 4
	block := t.layout.render(t.children, childWidth, t.version())
	locals := append([]int{0}, block.toggleLocals(childWidth, t.children)...)
	bodyLines := len(t.bodyLines(width))
	for i := 1; i < len(locals); i++ {
		locals[i] += bodyLines
	}
	return locals
}

func (t *thinkingItem) bodyLines(width int) []string {
	if t.text == "" {
		return nil
	}
	return renderContentBlock(width, contentBlock{
		kind:       contentMarkdown,
		text:       normalizeThinkingMarkdown(t.text),
		bodyPrefix: "  ",
		tone:       toneMuted,
		stripANSI:  true,
	})
}

func (t *thinkingItem) heading() string {
	if t.interrupted {
		return " thinking · interrupted"
	}
	return " thinking"
}

func (t *thinkingItem) render(width int) []string {
	var lines []string
	if !t.expanded {
		lines = []string{palette.Subtle.On("[") + palette.Primary.On("+") + palette.Subtle.On("]") + palette.Muted.On(t.heading())}
	} else {
		lines = append(lines, palette.Subtle.On("[")+palette.Primary.On("-")+palette.Subtle.On("]")+palette.Muted.On(t.heading()))
		lines = append(lines, t.bodyLines(width)...)
		lines = append(lines, t.layout.render(t.children, width-4, t.version()).lines...)
	}
	if t.nested {
		lines = prefixLines(lines, nestPad)
	}
	return indentLines(lines, t.depth)
}

// normalizeThinkingMarkdown repairs the common boundary produced when one
// bold reasoning heading closes immediately before the next one opens. Left
// untouched, the four adjacent markers leak into the transcript as "****".
func normalizeThinkingMarkdown(text string) string {
	return strings.ReplaceAll(text, "****", "**\n\n**")
}

// openTurn tracks one in-progress assistant turn for a task: the
// response headline (which accumulates all visible content), the current
// open thinking block, and loaded skills. Tool/edit groups for the turn
// are tracked separately (curTools/curEdits) but also render after the
// response.
type openTurn struct {
	resp   *assistantItem
	think  *thinkingItem
	skills *skillsItem
}
