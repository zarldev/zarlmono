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

// draw paints settings as an open full-screen utility: one identity row and
// divider, a category nav/detail split, and one contextual footer with transient
// save feedback. Persistence and focus behavior remain owned by settingsDialog.
func (d *settingsDialog) draw(scr uv.Screen, area uv.Rectangle) {
	l, ok := drawUtilitySplitPane(scr, area, settingsNavW)
	if !ok {
		return // too small to lay out
	}
	bodyH := l.Body.Dy()
	detailW := min(l.Detail.Dx(), settingsDetailMax)

	// Surface identity. Category navigation stays in the nav rail rather than
	// being duplicated as a second tab strip in the header.
	header := overlayTopBar("settings", nil, 0, d.cats[d.cat].name, l.Context.Dx())
	drawOverlayContext(scr, l, header, palette.Border)

	// Nav rail.
	catStart, catEnd := windowAroundCursor(d.cat, len(d.cats), bodyH)
	for i, c := range d.cats[catStart:catEnd] {
		catIndex := catStart + i
		if i >= bodyH {
			break
		}
		selected := catIndex == d.cat
		label := palette.Subtle.On(c.name)
		if selected && !d.focusRows {
			label = palette.Primary.On(c.name)
		} else if selected {
			label = palette.Assistant.On(c.name)
		}
		drawListRow(scr, uv.Rect(l.Nav.Min.X, l.Nav.Min.Y+i, l.Nav.Dx(), 1), label, selected, !d.focusRows)
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
			lines = append(lines, d.renderRow(row, d.focusRows && i == d.row, detailW))
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
		if strings.HasPrefix(ansi.Strip(ln), "├") {
			drawSectionRule(scr, l.Detail, l.Detail.Min.Y+i, ln)
			continue
		}
		drawLine(scr, uv.Rect(l.Detail.Min.X, l.Detail.Min.Y+i, detailW, 1), ln)
	}

	// Help panel: a description + default + resolved-source block below the
	// detail, filling the space the full screen gives us.
	if help := d.helpLines(detailW); len(help) > 0 {
		hy := l.Detail.Min.Y + len(lines) + 1
		for i, ln := range help {
			if hy+i >= l.Body.Max.Y {
				break
			}
			drawLine(scr, uv.Rect(l.Detail.Min.X, hy+i, detailW, 1), ln)
		}
	}

	// Single footer: stable keymap left, transient toast right.
	drawPaneRow(scr, l.Footer, " "+d.footerHint(), d.toast()+" ")
}

// helpLines is the description + default + resolved-source block for the
// detail panel. Row categories describe the selected setting; the inline
// panels (providers / gallery) get a one-line orientation.
func (d *settingsDialog) helpLines(width int) []string {
	cat := d.cats[d.cat]
	switch {
	case cat.providers:
		return []string{palette.Subtle.On("manage providers — api keys, oauth sign-in, custom openai-compatible backends.")}
	case cat.gallery:
		name, scope := d.themeSource()
		if d.gallery != nil && d.gallery.isPreviewing() {
			return []string{palette.Warning.On("previewing "+d.gallery.selectedName()) +
				palette.Muted.On(" · enter keeps it · esc restores "+d.gallery.origin)}
		}
		return []string{palette.Subtle.On("kept theme ") + palette.Muted.On(name+" · "+scope)}
	case cat.catalog:
		return []string{palette.Subtle.On("discovered agents / skills / hooks (read-only) — [ and ] switch between them.")}
	case cat.mcp:
		return []string{palette.Subtle.On("mcp servers — connected at launch; the agent can also mcp_connect ad-hoc. n: new · x: delete · t: toggle.")}
	}
	r := d.curRow()
	if r.desc == "" {
		return nil
	}
	head := "── " + r.label + " "
	if pad := width - len(head); pad > 0 {
		head += strings.Repeat("─", pad)
	}
	out := []string{palette.Border.On(head)}
	out = append(out, renderPlain(width, r.desc, withStyle(palette.Muted.On))...)
	out = append(out, palette.Subtle.On("built-in default ")+palette.Muted.On(r.def)+
		palette.Subtle.On("  ·  effective source ")+d.rowSourceLabel(r))
	return out
}

// rowSourceLabel reads where a row's value resolved from, for the help block.
func (d *settingsDialog) rowSourceLabel(r *settingsRow) string {
	if r.isSet {
		return scopeBadge(r.scope)
	}
	return palette.Subtle.On("built-in default")
}

// themeSource returns the persisted theme name + the scope it resolved from.
func (d *settingsDialog) themeSource() (string, string) {
	if d.s != nil && d.s.Svc != nil {
		if v, err := d.s.Svc.GetSetting(d.ctx, prefs.ScopeEffective, prefs.KeyTheme); err == nil && v.Value != "" {
			return v.Value, v.Source.String()
		}
	}
	return palette.Name, "default"
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
		return keyLegend(keyHint{"↑↓←→", "preview"}, keyHint{"enter", "keep"}, keyHint{"esc", "revert"})
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
	label := pad(row.label, labelW)

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

	marker, labelStyled := "  ", palette.Subtle.On(label)
	if sel {
		marker, labelStyled = palette.Primary.On("▸ "), palette.Assistant.On(label)
	}
	return rowLayout(marker+labelStyled+value, right, width)
}

func pad(s string, w int) string {
	for len(s) < w {
		s += " "
	}
	return s
}
