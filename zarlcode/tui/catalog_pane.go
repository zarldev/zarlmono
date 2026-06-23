package tui

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/catalog"
	"github.com/zarldev/zarlmono/zarlcode/engine"
)

// catalogKind distinguishes the inventories so one pane type can load,
// scaffold, and reload agents, skills, or hooks.
type catalogKind int

const (
	kindAgent catalogKind = iota
	kindSkill
	kindHook
)

// catalogRow is one entry in a read-only inventory pane (an agent or a skill):
// a name, a one-line description, optional metadata (agents carry
// provider/model/iteration info, skills don't), the source path, and the full
// markdown body revealed when the row is expanded.
type catalogRow struct {
	name, desc, meta, source, body string
}

// catalogPane is the detail panel for the Agents / Skills settings categories:
// a list of discovered definitions with an expandable body drawer, plus
// management (new / edit-in-$EDITOR / delete). The on-disk markdown files are
// the source of truth; the pane reloads from them after every change.
type catalogPane struct {
	noun     string // "agent" / "skill" — for the empty state + help line
	kind     catalogKind
	wsRoot   string
	rows     []catalogRow
	loadErr  string // first discovery error, surfaced dim under the list
	cursor   int
	expanded bool // body drawer open on the focused row

	naming bool     // inline "new" name prompt is open
	nameEd composer // the name being typed for a new definition

	status   string
	statusAt time.Time
}

func newAgentsPane(s *engine.Settings) *catalogPane {
	p := &catalogPane{noun: "agent", kind: kindAgent, wsRoot: wsRootOf(s)}
	p.reload(s)
	return p
}

func newSkillsPane(s *engine.Settings) *catalogPane {
	p := &catalogPane{noun: "skill", kind: kindSkill, wsRoot: wsRootOf(s)}
	p.reload(s)
	return p
}

func newHooksPane(s *engine.Settings) *catalogPane {
	p := &catalogPane{noun: "hook", kind: kindHook, wsRoot: wsRootOf(s)}
	p.reload(s)
	return p
}

// reload re-reads the on-disk inventory and rebuilds the rows, preserving the
// cursor where possible. Called at construction and after every edit/delete.
func (p *catalogPane) reload(s *engine.Settings) {
	if s != nil {
		p.wsRoot = wsRootOf(s)
	}
	p.rows = p.rows[:0]
	var errs []error
	switch p.kind {
	case kindAgent:
		agents, e := catalog.LoadAgents(p.wsRoot)
		errs = e
		for _, a := range agents {
			p.rows = append(p.rows, catalogRow{name: a.Name, desc: a.Description, meta: agentMeta(a), source: a.Source, body: a.Body})
		}
	case kindSkill:
		skills, e := catalog.LoadSkills(p.wsRoot)
		errs = e
		for _, sk := range skills {
			p.rows = append(p.rows, catalogRow{name: sk.Name, desc: sk.Description, source: sk.Source, body: sk.Body})
		}
	case kindHook:
		hks, e := catalog.LoadHooks(p.wsRoot)
		errs = e
		for _, h := range hks {
			p.rows = append(p.rows, catalogRow{name: h.Name, desc: h.Description, meta: hookMeta(h), source: h.Source, body: h.Command})
		}
	}
	p.loadErr = firstErr(errs)
	if p.cursor >= len(p.rows) {
		p.cursor = len(p.rows) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	p.expanded = false
}

// inSubMode reports whether the name prompt is open, so the host knows esc/left
// should cancel it rather than return focus to the category nav.
func (p *catalogPane) inSubMode() bool { return p.naming }

func (p *catalogPane) cur() (catalogRow, bool) {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return catalogRow{}, false
	}
	return p.rows[p.cursor], true
}

func (p *catalogPane) setStatus(s string) { p.status, p.statusAt = s, time.Now() }

func wsRootOf(s *engine.Settings) string {
	if s == nil {
		return ""
	}
	return s.WorkspaceRoot()
}

// agentMeta is the dim right-aligned summary for an agent row:
// provider/model/iterations/thinking, omitting fields the agent inherits.
func agentMeta(a catalog.Agent) string {
	var parts []string
	if a.Provider != "" {
		parts = append(parts, a.Provider)
	}
	if a.Model != "" {
		parts = append(parts, a.Model)
	}
	if a.MaxIterations > 0 {
		parts = append(parts, strconv.Itoa(a.MaxIterations)+" iters")
	}
	if a.Thinking {
		parts = append(parts, "thinking")
	}
	return strings.Join(parts, " · ")
}

// hookMeta is the dim right-aligned summary for a hook row: when it fires,
// what it matches, and whether it can block the call.
func hookMeta(h catalog.Hook) string {
	parts := []string{string(h.Event)}
	if h.Matcher != "" {
		parts = append(parts, h.Matcher)
	}
	if h.Blocking {
		parts = append(parts, "blocking")
	}
	return strings.Join(parts, " · ")
}

func firstErr(errs []error) string {
	if len(errs) == 0 {
		return ""
	}
	return errs[0].Error()
}

// handleKey drives list navigation, body expansion, and management (new /
// edit / delete / reload). It returns an action so the host can turn an edit
// request into the editor-launch command. Collapse / return-to-nav is the
// host's concern (left/esc), so this only moves the cursor, opens the drawer,
// and emits intents.
func (p *catalogPane) handleKey(msg tea.KeyPressMsg) action {
	if p.naming {
		return p.handleNameKey(msg)
	}
	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
			p.expanded = false
		}
	case "down", "j":
		if p.cursor < len(p.rows)-1 {
			p.cursor++
			p.expanded = false
		}
	case "home", "g":
		p.cursor, p.expanded = 0, false
	case "end", "G":
		if len(p.rows) > 0 {
			p.cursor, p.expanded = len(p.rows)-1, false
		}
	case "enter", "space", " ", "right", "l":
		if len(p.rows) > 0 {
			p.expanded = true
		}
	case "e":
		if r, ok := p.cur(); ok && r.source != "" {
			return actionEditFile{path: r.source}
		}
	case "n":
		p.naming = true
		p.nameEd = composer{}
		p.setStatus("")
	case "x", "delete":
		return p.deleteCur()
	case "r":
		p.reload(nil)
		p.setStatus("reloaded")
	}
	return actionNone{}
}

// handleNameKey drives the inline "new <noun>" name prompt; enter scaffolds the
// file (and returns an edit intent), esc cancels.
func (p *catalogPane) handleNameKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc":
		p.naming = false
	case "enter":
		return p.submitNew()
	case "backspace":
		p.nameEd.backspace()
	case "left":
		p.nameEd.left()
	case "right":
		p.nameEd.right()
	default:
		if msg.Text != "" {
			p.nameEd.insert(msg.Text)
		}
	}
	return actionNone{}
}

// submitNew scaffolds a templated definition for the typed name and returns an
// intent to open it in the editor. An existing name opens the existing file
// rather than clobbering it.
func (p *catalogPane) submitNew() action {
	name := strings.TrimSpace(p.nameEd.text())
	if name == "" {
		p.setStatus("name required")
		return actionNone{}
	}
	var path string
	var err error
	switch p.kind {
	case kindAgent:
		path, err = catalog.ScaffoldAgent(name)
	case kindSkill:
		path, err = catalog.ScaffoldSkill(name)
	case kindHook:
		path, err = catalog.ScaffoldHook(name)
	}
	p.naming = false
	switch {
	case errors.Is(err, catalog.ErrExists):
		p.setStatus(name + " exists — opening it")
		return actionEditFile{path: path}
	case err != nil:
		p.setStatus("new: " + err.Error())
		return actionNone{}
	}
	p.setStatus(name + " created")
	return actionEditFile{path: path}
}

// deleteCur removes the selected definition's file and reloads.
func (p *catalogPane) deleteCur() action {
	r, ok := p.cur()
	if !ok || r.source == "" {
		return actionNone{}
	}
	if err := os.Remove(r.source); err != nil {
		p.setStatus("delete: " + err.Error())
		return actionNone{}
	}
	p.setStatus(r.name + " deleted")
	p.reload(nil)
	return actionNone{}
}

// detailLines renders the inventory: one row per entry (cursor-marked, meta
// right-aligned), with the focused row's source + body inserted beneath when
// expanded. An empty inventory shows a dim placeholder; a load error trails.
// detailLines renders the inventory windowed to `height` lines so the focused
// row stays visible when the list overflows the detail region. height <= 0 (or
// a list that fits) renders everything from the top, unchanged.
func (p *catalogPane) detailLines(width, height int) []string {
	if p.naming {
		return p.nameFormLines()
	}
	if len(p.rows) == 0 {
		out := []string{
			palette.Subtle.On("(no " + p.noun + "s discovered)"),
			palette.Muted.On("press n to create one."),
		}
		if p.loadErr != "" {
			out = append(out, palette.Warning.On("⚠ "+p.loadErr))
		}
		return out
	}

	// Pick the first visible row. A small list (the common case) starts at the
	// top unchanged; an overflowing list windows around the cursor; an expanded
	// row anchors near the top so its body fills the space below.
	var start int
	switch {
	case height < 1 || len(p.rows) <= height:
		// fits (or no budget) — render from the top
	case p.expanded:
		start = min(p.cursor, len(p.rows)-1)
	default:
		start, _ = windowAroundCursor(p.cursor, len(p.rows), height)
	}

	var out []string
	for i := start; i < len(p.rows); i++ {
		r := p.rows[i]
		sel := i == p.cursor
		marker, name := "  ", palette.Subtle.On(r.name)
		if sel {
			marker, name = palette.Primary.On("▸ "), palette.Assistant.On(r.name)
		}
		left := marker + name
		if r.desc != "" {
			left += "  " + palette.Muted.On(r.desc)
		}
		var right string
		if r.meta != "" {
			right = palette.Subtle.On(r.meta)
		}
		out = append(out, rowLayout(left, right, width))
		if sel && p.expanded {
			out = append(out, p.bodyLines(r, width)...)
		}
		// The cursor row is always within the first `height` rows of `start`, so
		// once the viewport is full it's already been emitted — stop building.
		if height >= 1 && len(out) >= height {
			break
		}
	}
	if p.loadErr != "" {
		out = append(out, "", palette.Warning.On("⚠ "+p.loadErr))
	}
	return out
}

func (p *catalogPane) bodyLines(r catalogRow, width int) []string {
	var out []string
	if r.source != "" {
		out = append(out, palette.Subtle.On("    "+shortenHome(r.source)))
	}
	body := r.body
	if body == "" {
		body = "(no body)"
	}
	out = append(out, renderContentBlock(width, contentBlock{
		kind:       contentMarkdown,
		text:       body,
		bodyPrefix: "    ",
		tone:       toneMuted,
		stripANSI:  true,
		cacheKey:   "catalog:" + r.name,
	})...)
	return out
}

// nameFormLines renders the inline "new <noun>" name prompt.
func (p *catalogPane) nameFormLines() []string {
	val := string(p.nameEd.value[:p.nameEd.cursor]) +
		palette.Primary.On("▏") + string(p.nameEd.value[p.nameEd.cursor:])
	return []string{
		palette.Assistant.On("new " + p.noun),
		"",
		palette.Subtle.On(pad("name", 8)) + val,
		"",
		palette.Muted.On("creates ~/.zarlcode/config/" + p.noun + "s/<name>.md, then opens $EDITOR"),
	}
}

// footerHint is the key legend the host shows while a catalog pane is focused.
func (p *catalogPane) footerHint() string {
	switch {
	case p.naming:
		return keyLegend(keyHint{label: "name"}, keyHint{"enter", "create"}, keyHint{"esc", "cancel"})
	case p.expanded:
		return keyLegend(keyHint{"↑↓", "move"}, keyHint{"←/esc", "collapse"}, keyHint{"e", "edit"}, keyHint{"x", "delete"})
	}
	return keyLegend(keyHint{"↑↓", "move"}, keyHint{"→", "expand"}, keyHint{"n", "new"},
		keyHint{"e", "edit"}, keyHint{"x", "delete"}, keyHint{"esc", "back"})
}

// catalogDialog wraps the agents + skills + hooks inventory panes as a modal
// overlay. tab cycles between them; esc/q close; every other key delegates to
// the focused pane. The panes share the same settings handle for root
// resolution and reload-on-edit.
type catalogDialog struct {
	panes [3]*catalogPane // agents, skills, hooks — indexed by tab
	tab   int
	s     *engine.Settings
}

func newCatalogDialog(s *engine.Settings) *catalogDialog {
	return &catalogDialog{
		panes: [3]*catalogPane{newAgentsPane(s), newSkillsPane(s), newHooksPane(s)},
		s:     s,
	}
}

func (d *catalogDialog) activePane() *catalogPane {
	return d.panes[d.tab]
}

func (d *catalogDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "q", "ctrl+k":
		return actionClose{}
	case "tab":
		d.tab = (d.tab + 1) % len(d.panes)
		return actionNone{}
	}
	act := d.activePane().handleKey(msg)
	// After an edit action (n→scaffold→edit, e→edit, x→delete), reload every
	// pane from disk so the lists stay in sync.
	if _, ok := act.(actionEditFile); ok {
		for _, p := range d.panes {
			p.reload(d.s)
		}
	}
	return act
}

func (d *catalogDialog) draw(scr uv.Screen, area uv.Rectangle) {
	w, h := area.Dx(), area.Dy()
	if w < 50 || h < 12 {
		return
	}
	l, ok := drawSplitPane(scr, area, "catalog", 28)
	if !ok {
		return
	}
	counts := make([]int, len(d.panes))
	for i, p := range d.panes {
		for _, r := range p.rows {
			if r.name != "" {
				counts[i]++
			}
		}
	}
	left := overlayTopBar("catalog", []string{"agents", "skills", "hooks"}, d.tab, fmt.Sprintf("%d agents · %d skills · %d hooks", counts[0], counts[1], counts[2]), l.Context.Dx())
	drawOverlayContext(scr, l, left, palette.Subtle.On("ctrl+k close "), palette.Border)
	pane := d.activePane()
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y, l.Nav.Dx(), 1), palette.Muted.On(" inventory and local definitions"))
	drawLine(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y+1, l.Nav.Dx(), 1), palette.Border.On(strings.Repeat("─", l.Nav.Dx())))
	detailY := l.Nav.Min.Y + 2
	detailH := max(0, l.Nav.Dy()-2)
	lines := pane.detailLines(l.Nav.Dx(), detailH)
	for i, line := range lines {
		if i >= detailH {
			break
		}
		drawLine(scr, uv.Rect(l.Nav.Min.X, detailY+i, l.Nav.Dx(), 1), ansi.Truncate(line, l.Nav.Dx(), ""))
	}
	selection := []string{
		sectionHead("selection", l.Detail.Dx()),
		palette.Muted.On("browse and edit agent, skill, and hook definitions"),
	}
	for i, line := range selection {
		if i >= l.Detail.Dy() {
			break
		}
		drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+i, l.Detail.Dx(), 1), ansi.Truncate(line, l.Detail.Dx(), ""))
	}
	if pane.status != "" && time.Since(pane.statusAt) < 3*time.Second && l.Detail.Dy() > 2 {
		drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+2, l.Detail.Dx(), 1), ansi.Truncate(palette.Muted.On(pane.status), l.Detail.Dx(), ""))
	}
	hint := pane.footerHint() + "  " + keyLegend(keyHint{"tab", "switch"}, keyHint{"esc", "close"})
	drawPaneRow(scr, l.Footer, palette.Subtle.On(" "+hint), "")
}

func (d *catalogDialog) title() string {
	counts := make([]int, len(d.panes))
	for i, p := range d.panes {
		for _, r := range p.rows {
			if r.name != "" {
				counts[i]++
			}
		}
	}
	return fmt.Sprintf(" catalog  %d agents · %d skills · %d hooks ", counts[0], counts[1], counts[2])
}
