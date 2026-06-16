package tui

// thinkingItem is the reasoning block for an assistant turn. Reasoning
// deltas arrive on the runner's out-of-band Thinking channel
// (appendThinking) and accumulate here across iterations — one block
// per turn. Collapsed by default ([+] thinking); browse + enter
// expands.
type thinkingItem struct {
	versioned
	depth    int
	nested   bool // turn activity: rendered tight (no blank line above)
	text     string
	expanded bool
	done     bool
}

func (t *thinkingItem) finished() bool { return t.done }

func (t *thinkingItem) toggle() {
	t.expanded = !t.expanded
	t.bump()
}

// togglerAt: the "[+]/[-] thinking" header is local line 0 (the nestPad/indent
// prefixes don't shift line indices); everything below is body.
func (t *thinkingItem) togglerAt(_, ln int) toggler {
	if ln == 0 {
		return t
	}
	return nil
}

func (t *thinkingItem) render(width int) []string {
	var lines []string
	if !t.expanded {
		lines = []string{palette.Subtle.On("[") + palette.Primary.On("+") + palette.Subtle.On("]") + palette.Muted.On(" thinking")}
	} else {
		lines = append(lines, palette.Subtle.On("[")+palette.Primary.On("-")+palette.Subtle.On("]")+palette.Muted.On(" thinking"))
		lines = append(lines, renderContentBlock(width-2-t.depth*2, contentBlock{
			kind:       contentMarkdown,
			text:       t.text,
			bodyPrefix: "  ",
			tone:       toneMuted,
			stripANSI:  true,
		})...)
	}
	if t.nested {
		lines = prefixLines(lines, nestPad)
	}
	return indentLines(lines, t.depth)
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
