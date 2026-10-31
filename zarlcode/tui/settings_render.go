package tui

import (
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/prefs"
)

const (
	settingsNavW      = 16
	settingsDetailMax = 92              // cap detail width so badges don't sprawl on ultrawide
	settingsToastTTL  = 4 * time.Second // how long a footer toast lingers
)

// fullScreen marks the settings overlay as a full-screen takeover, so the root
// suppresses the panes + global status bar behind it (one footer, not two).
func (d *settingsDialog) fullScreen() bool { return true }

// draw paints individually headed settings panes and a single contextual footer.
// Category and help headings use the same frame/section rules as other panes.
func (d *settingsDialog) draw(scr uv.Screen, area uv.Rectangle) {
	if area.Dx() < 12 || area.Dy() < 5 {
		return
	}
	navW := min(settingsNavW+2, area.Dx()/3)
	paneH := area.Dy() - 1
	navLabel, detailLabel := palette.Primary, palette.Subtle
	if d.focusRows {
		navLabel, detailLabel = detailLabel, navLabel
	}
	nav := drawFrame(scr, uv.Rect(area.Min.X, area.Min.Y, navW, paneH), frameStyle{
		Label:  ansi.Truncate("settings", navW-4, "…"),
		Border: palette.Border, LabelColor: navLabel,
	})
	detail := drawFrame(scr, uv.Rect(area.Min.X+navW, area.Min.Y, area.Dx()-navW, paneH), frameStyle{
		Label:  ansi.Truncate(d.cats[d.cat].name, area.Dx()-navW-4, "…"),
		Border: palette.Border, LabelColor: detailLabel,
	})
	footer := uv.Rect(area.Min.X, area.Max.Y-1, area.Dx(), 1)
	detailW := min(detail.Dx(), settingsDetailMax)
	bodyH := detail.Dy()

	// Nav rail.
	catStart, catEnd := windowAroundCursor(d.cat, len(d.cats), nav.Dy())
	style := terminalChrome()
	for i, c := range d.cats[catStart:catEnd] {
		catIndex := catStart + i
		if i >= nav.Dy() {
			break
		}
		selected := catIndex == d.cat
		drawLine(scr, uv.Rect(nav.Min.X, nav.Min.Y+i, nav.Dx(), 1), listRow(style, c.name, "", nav.Dx(), selected))
	}

	// Detail: Providers and Appearance render their own inline panels; every
	// other category renders its setting rows.
	var lines []string
	switch {
	case d.cats[d.cat].providers:
		lines = d.providers.detailLines(detailW)
	case d.cats[d.cat].gallery:
		lines = d.gallery.detailLines(detailW, bodyH)
	case d.cats[d.cat].catalog:
		lines = d.catalogPane.detailLines(detailW, bodyH)
	case d.cats[d.cat].mcp:
		lines = d.mcp.detailLines(detailW)
	default:
		var section string
		selectedLine := 0
		for i, row := range d.rows() {
			if row.section != "" && row.section != section {
				lines = append(lines, sectionHead(row.section, detailW))
				section = row.section
			}
			if i == d.row {
				selectedLine = len(lines)
			}
			lines = append(lines, d.renderRow(row, i == d.row, detailW))
		}
		if len(lines) > bodyH {
			start, end := windowAroundCursor(selectedLine, len(lines), bodyH)
			lines = lines[start:end]
		}
	}
	for i, ln := range lines {
		if i >= bodyH {
			break
		}
		drawLine(scr, uv.Rect(detail.Min.X, detail.Min.Y+i, detailW, 1), ln)
	}

	// Help panel: a description + default + resolved-source block below the
	// detail, filling the space the full screen gives us.
	if help := d.helpLines(detailW); len(help) > 0 {
		hy := detail.Min.Y + len(lines) + 1
		for i, ln := range help {
			if hy+i >= detail.Max.Y {
				break
			}
			drawLine(scr, uv.Rect(detail.Min.X, hy+i, detailW, 1), ln)
		}
	}

	// Single footer: stable keymap left, transient toast right.
	if toast := d.toast(); toast != "" {
		drawPaneRow(scr, footer, " "+toast, "")
	} else {
		drawPaneRow(scr, footer, " "+d.footerHint(), "")
	}
}

// helpLines is the description + default + resolved-source block for the
// detail panel. Row categories describe the selected setting; the inline
// panels (providers / gallery) get a one-line orientation.
func (d *settingsDialog) helpLines(width int) []string {
	cat := d.cats[d.cat]
	helpHead := sectionHead("help", width)
	switch {
	case cat.providers:
		return []string{helpHead, palette.Subtle.On("manage providers — api keys, oauth sign-in, custom openai-compatible backends.")}
	case cat.gallery:
		name, scope := d.themeSource()
		line := "effective: " + name + " (" + scope + ") · save scope: " + d.gallery.scope
		if d.gallery.isPreviewing() {
			line += " · previewing " + d.gallery.selectedName()
		}
		return []string{helpHead, palette.Subtle.On(line)}
	case cat.catalog:
		return []string{helpHead, palette.Subtle.On("discovered agents / skills / hooks (read-only) — [ and ] switch between them.")}
	case cat.mcp:
		return []string{helpHead, palette.Subtle.On("mcp servers — connected at launch; the agent can also mcp_connect ad-hoc. n: new · x: delete · t: toggle.")}
	}
	r := d.curRow()
	if r.desc == "" {
		return nil
	}
	out := []string{sectionHead(r.label, width)}
	out = append(out, renderPlain(width, r.desc, withStyle(palette.Muted.On))...)
	builtin := r.def
	if r.builtin != "" {
		builtin = r.builtin
	}
	out = append(out, palette.Subtle.On("built-in default ")+palette.Muted.On(builtin)+
		palette.Subtle.On("  ·  effective source ")+d.rowSourceLabel(r))
	return out
}

// rowSourceLabel reads where a row's value resolved from, for the help block.
func (d *settingsDialog) rowSourceLabel(r *settingsRow) string {
	if r.isSet {
		return scopeBadge(r.scope)
	}
	if r.builtin != "" && r.def == compactActiveSentinel {
		return palette.Subtle.On("active target")
	}
	return palette.Subtle.On("built-in default")
}

// themeSource returns the persisted theme name + the scope it resolved from.
func (d *settingsDialog) themeSource() (string, string) {
	if d.gallery.effective.Name != "" {
		return d.gallery.effective.Name, d.gallery.effective.Scope
	}
	return palette.Name, "unavailable"
}

// footerHint is the context-sensitive key legend: the focused pane and its
// sub-mode decide what's actionable.
func (d *settingsDialog) footerHint() string {
	switch {
	case d.editing:
		return keyLegend(keyHint{label: "type"}, keyHint{"enter", "save"}, keyHint{"esc", "cancel"})
	case d.cats[d.cat].providers && d.focusRows:
		return d.providers.footerHint()
	case d.cats[d.cat].gallery && d.focusRows:
		return keyLegend(keyHint{"arrows", "preview"}, keyHint{"tab", "scope"}, keyHint{"enter", "save"}, keyHint{"r", "inherit"}, keyHint{"esc", "revert"})
	case d.cats[d.cat].catalog && d.focusRows:
		return d.catalogPane.footerHint()
	case d.cats[d.cat].mcp && d.focusRows:
		return d.mcp.footerHint()
	case !d.focusRows:
		return keyLegend(keyHint{"↑↓", "category"}, keyHint{"→/enter", "open"}, keyHint{"esc", "done"})
	}
	switch d.curRow().kind {
	case rowAction:
		return keyLegend(keyHint{"enter", "open"}, keyHint{"↑↓", "move"}, keyHint{"←", "nav"}, keyHint{"esc", "done"})
	case rowEnum, rowModel:
		hints := []keyHint{{"enter", "choose"}}
		if d.curRow().isSet && d.curRow().scope == prefs.ScopeWorkspace {
			hints = append(hints, keyHint{"p", "make global"})
		}
		hints = append(hints, keyHint{"↑↓", "move"}, keyHint{"←", "nav"}, keyHint{"esc", "done"})
		return keyLegend(hints...)
	default:
		hints := []keyHint{{"enter", "edit"}}
		if d.curRow().isSet && d.curRow().scope == prefs.ScopeWorkspace {
			hints = append(hints, keyHint{"p", "make global"})
		}
		hints = append(hints, keyHint{"↑↓", "move"}, keyHint{"←", "nav"}, keyHint{"esc", "done"})
		return keyLegend(hints...)
	}
}

// toast returns the active status message styled for the footer-right, or ""
// once it's aged past the TTL. When the providers pane is focused its status
// takes over (so provider actions report in the same slot).
func (d *settingsDialog) toast() string {
	text, at := d.status, d.statusAt
	if d.cats[d.cat].providers && d.focusRows {
		text, at = d.providers.status, d.providers.statusAt
	}
	if d.cats[d.cat].mcp && d.focusRows {
		text, at = d.mcp.status, d.mcp.statusAt
	}
	if d.cats[d.cat].catalog && d.focusRows {
		text, at = d.catalogPane.status, d.catalogPane.statusAt
	}
	if text == "" || time.Since(at) > settingsToastTTL {
		return ""
	}
	if isErrorStatus(text) {
		return renderFooterToast("✗ "+text, toastError)
	}
	return renderFooterToast("✓ "+text, toastSuccess)
}

// renderRow formats one detail row as "marker label value … badges", with the
// scope (and any model-fetch hint) right-aligned. While the row is being
// edited the inline editor stands in for the value.
func (d *settingsDialog) renderRow(row settingsRow, sel bool, width int) string {
	const labelW = 22
	label := pad(strings.ToLower(row.label), labelW)

	var value, right string
	switch {
	case row.kind == rowAction:
		value = palette.Subtle.On("↵")
	case sel && d.editing:
		value = "› " + string(d.editor.value[:d.editor.cursor]) +
			palette.Primary.On("▏") + string(d.editor.value[d.editor.cursor:])
	case row.kind == rowModel:
		hint := d.modelHintFor(d.providerForRow(row.key))
		if row.isSet && row.value != "" {
			value = row.value
			right = joinBadges(hint, scopeBadge(row.scope))
		} else {
			value = palette.Subtle.On(row.def)
			right = hint
		}
	case row.kind == rowKey && row.isSet:
		value = palette.Subtle.On("••••••")
		right = scopeBadge(row.scope)
	case row.isSet && row.value != "":
		value = row.value
		right = scopeBadge(row.scope)
	case row.value == "" && row.isSet:
		value = palette.Subtle.On("(cleared)")
	default:
		value = palette.Subtle.On(row.def)
	}

	return listRow(terminalChrome(), label+value, right, width, sel)
}

func pad(s string, w int) string {
	for len(s) < w {
		s += " "
	}
	return s
}
