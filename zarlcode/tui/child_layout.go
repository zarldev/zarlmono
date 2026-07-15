package tui

// childIndent is the two-space gutter every nested child row carries under a
// collapsible container's header.
const childIndent = "  "

// childBlock is the rendered form of a collapsible container's expanded
// children: the indented child lines, plus the line offset of each child's
// header within the container's block (line 0 is always the container's own
// header).
//
// groupItem and subAgentItem render, hit-test, and keyboard-select their
// children through renderChildBlock so the child-offset math lives in exactly
// one place. When render and hit-testing computed offsets independently a
// drift would make a click toggle the wrong row; routing both through this
// makes that class of bug unrepresentable.
type childBlock struct {
	lines   []string
	offsets []int
	sizes   []int
}

// renderChildBlock renders children at childWidth, prefixes each line with the
// gutter, and records where each child's header lands. childWidth is the
// container's responsibility (it knows how much it reserves for the gutter).
func renderChildBlock(children []item, childWidth int) childBlock {
	cb := childBlock{offsets: make([]int, 0, len(children)), sizes: make([]int, 0, len(children))}
	off := 1 // line 0 is the container header
	for _, c := range children {
		cb.offsets = append(cb.offsets, off)
		rendered := c.render(childWidth)
		cb.sizes = append(cb.sizes, len(rendered))
		for _, l := range rendered {
			cb.lines = append(cb.lines, childIndent+l)
		}
		off += len(rendered)
	}
	return cb
}

// togglerForLine returns the toggler whose header sits on local line ln within
// the block, or nil for the container's body lines (only a child's first line
// is a toggle target). bump is the container's version bump — children render
// inline, so the container must re-render for a child's new state to show.
func (cb childBlock) togglerForLine(ln int, childWidth int, children []item, bump func()) toggler {
	for k, off := range cb.offsets {
		if ln == off {
			tg, ok := children[k].(toggler)
			if !ok {
				return nil
			}
			return toggleChildAndParent(tg, bump)
		}
		if ln > off && ln < off+cb.sizes[k] {
			if ht, ok := children[k].(hitToggler); ok {
				return ht.togglerAt(childWidth, ln-off)
			}
		}
	}
	return nil
}

func toggleChildAndParent(child toggler, bump func()) toggler {
	return togglerFunc(func() {
		child.toggle()
		bump()
	})
}
