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
	lines    []string
	rawLines []string
	offsets  []int
	sizes    []int
}

// childBlockCache memoizes one container's complete child layout. The owner
// version is the invalidation boundary: child toggles and runner events bump the
// inline-rendering owner, while width/theme changes invalidate presentation.
type childBlockCache struct {
	width    int
	ver      uint64
	gen      uint64
	block    childBlock
	valid    bool
	children []childRenderEntry
}

func (c *childBlockCache) render(children []item, width int, ver uint64) childBlock {
	if c.valid && c.width == width && c.ver == ver && c.gen == themeGen {
		return c.block
	}
	if c.width != width || c.gen != themeGen {
		clear(c.children)
	}
	if len(c.children) > len(children) {
		clear(c.children[len(children):])
		c.children = c.children[:len(children)]
	}
	for len(c.children) < len(children) {
		c.children = append(c.children, childRenderEntry{})
	}
	block := childBlock{offsets: make([]int, 0, len(children)), sizes: make([]int, 0, len(children))}
	for i, child := range children {
		entry := c.children[i]
		if entry.item != child || entry.version != child.version() {
			raw := child.render(width)
			entry = childRenderEntry{item: child, version: child.version(), raw: raw, indented: prefixLines(raw, childIndent)}
			c.children[i] = entry
		}
		block.offsets = append(block.offsets, 1+len(block.rawLines))
		block.sizes = append(block.sizes, len(entry.raw))
		block.rawLines = append(block.rawLines, entry.raw...)
		block.lines = append(block.lines, entry.indented...)
	}
	c.width, c.ver, c.gen = width, ver, themeGen
	c.block = block
	c.valid = true
	return c.block
}

// Child entries belong to this container, not a global cache. Removed or hidden
// children release their rendered rows; identity protects reordered insertions.
type childRenderEntry struct {
	item     item
	version  uint64
	raw      []string
	indented []string
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
		cb.rawLines = append(cb.rawLines, rendered...)
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
				if nested := ht.togglerAt(childWidth, ln-off); nested != nil {
					return toggleChildAndParent(nested, bump)
				}
			}
		}
	}
	return nil
}

// toggleLocals returns every disclosure-header line in the block, including
// disclosures nested inside expanded children. Each child owns the layout of its
// descendants; this block only translates those local lines into its coordinates.
func (cb childBlock) toggleLocals(childWidth int, children []item) []int {
	locals := make([]int, 0, len(children))
	for k, off := range cb.offsets {
		locals = append(locals, off)
		child, ok := children[k].(toggleLocator)
		if !ok {
			continue
		}
		for _, local := range child.toggleLocals(childWidth) {
			if local > 0 {
				locals = append(locals, off+local)
			}
		}
	}
	return locals
}

func toggleChildAndParent(child toggler, bump func()) toggler {
	return childToggler{child: child, bump: bump}
}

// childToggler preserves optional tool-view controls across each container's
// invalidation boundary, just as the ordinary disclosure action does.
type childToggler struct {
	child toggler
	bump  func()
}

func (t childToggler) toggle() {
	t.child.toggle()
	t.bump()
}

func (t childToggler) applyToolViewKey(key string) bool {
	control, ok := t.child.(toolViewController)
	if !ok || !control.applyToolViewKey(key) {
		return false
	}
	t.bump()
	return true
}
