package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/llm/litellm"
)

type targetModelInfoMsg struct {
	generation engine.TargetGeneration
	err        error
}

// OpenTargetModelInfo opens the active construction's cached facts and gateway
// declarations. It does no I/O; r explicitly requests bounded optional enrichment.
// The same detail is available under settings > model > gateway advertised.
func (m *UI) OpenTargetModelInfo() {
	if m.live != nil {
		m.overlay.push(newTargetModelInfoDialog(m.live.RunTarget()))
	}
}

// RefreshTargetModelInfo returns a bounded command for optional enrichment from
// the actual active provider. Constructing the command does no I/O. Execute it
// off the event loop and deliver its result to Update; old generations are ignored.
// A denial never changes model choices, operational budgets, or inference policy.
func (m *UI) RefreshTargetModelInfo() tea.Cmd {
	if m.live == nil {
		return nil
	}
	target := m.live.RunTarget()
	if target.Facts == nil {
		return nil
	}
	facts := target.Facts.Clone()
	parent := m.appContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, providerModelFetchTimeout)
		defer cancel()
		_, err := facts.RefreshModelInfo(ctx)
		return targetModelInfoMsg{generation: facts.Generation, err: err}
	}
}

func (m *UI) handleTargetModelInfoMsg(msg tea.Msg) bool {
	result, ok := msg.(targetModelInfoMsg)
	if !ok {
		return false
	}
	if m.live == nil {
		return true
	}
	target := m.live.RunTarget()
	if target.Facts == nil || target.Facts.Generation != result.generation {
		return true
	}
	for _, overlay := range m.overlay.stack {
		if d, ok := overlay.(*targetModelInfoDialog); ok && d.target.Facts != nil && d.target.Facts.Generation == result.generation {
			d.err = result.err
		}
	}
	return true
}

type targetModelInfoDialog struct {
	target        engine.RunTarget
	err           error
	scroll        int
	viewport      int
	targetChanged bool
}

func newTargetModelInfoDialog(target engine.RunTarget) *targetModelInfoDialog {
	return &targetModelInfoDialog{target: target.Clone()}
}

func (d *targetModelInfoDialog) matches(target engine.RunTarget) bool {
	return d.target.Facts != nil && target.Facts != nil && d.target.Facts.Generation == target.Facts.Generation
}

func (d *targetModelInfoDialog) handleKey(msg tea.KeyPressMsg) action {
	switch msg.String() {
	case "esc", "q":
		return actionClose{}
	case "r":
		return actionFetchModels{provider: d.target.Spec.Name}
	case "up", "k":
		d.scroll = max(0, d.scroll-1)
	case "down", "j":
		d.scroll++
	case "pgup":
		d.scroll = max(0, d.scroll-max(1, d.viewport-1))
	case "pgdown":
		d.scroll += max(1, d.viewport-1)
	case "home":
		d.scroll = 0
	case "end":
		d.scroll = len(d.lines())
	}
	return actionNone{}
}

func (d *targetModelInfoDialog) lines() []string {
	lines := []string{
		"Prepared requested alias: " + strconv.Quote(d.target.Spec.Model),
		"Operational budget: " + strconv.Itoa(d.target.Window) + " tokens (not a model ceiling)",
	}
	if d.targetChanged {
		lines = append(lines, "Active target changed; reopen this construction snapshot to refresh")
	}
	if d.target.Facts == nil {
		return append(lines, "Construction facts unavailable")
	}
	facts := d.target.Facts
	lines = append(lines,
		"Budget source: "+facts.Budget.Source.String(),
		"Selected codec: "+facts.SelectedProtocol.String()+" (override intent: "+d.target.Spec.Protocol.String()+")",
		fmt.Sprintf("Model binding: incomplete/unknown unless proven; %d unresolved issues", len(facts.Resolution.Issues)),
		"Gateway advertised — declarations, not grants or effective capabilities",
		"Currency unknown · applicability unknown · not an exhaustive model list",
	)
	if d.err != nil {
		status := "Optional metadata refresh unavailable; inference and model list unaffected"
		if !errors.Is(d.err, litellm.ErrModelInfoUnavailable) && !errors.Is(d.err, engine.ErrTargetModelInfoUnavailable) {
			status = "Optional metadata refresh unsuccessful; last-good cache retained"
		}
		lines = append(lines, status)
	}
	snapshot, err := facts.CachedModelInfo()
	if err != nil {
		return append(lines, "No cached gateway declarations · r to refresh (optional)")
	}
	freshness := "cached"
	if !time.Now().Before(snapshot.Provenance.ExpiresAt) {
		freshness = "stale last-good"
	}
	lines = append(lines, fmt.Sprintf("%s · observed %s · expires %s", freshness,
		snapshot.Provenance.ObservedAt.Format(time.RFC3339), snapshot.Provenance.ExpiresAt.Format(time.RFC3339)))
	for i, row := range snapshot.Rows {
		lines = append(lines,
			fmt.Sprintf("Row %d · alias %s · deployment %s", i+1, strconv.Quote(string(row.RequestedModel)), strconv.Quote(string(row.Deployment))),
			"  Declared upstream: "+strconv.Quote(string(row.UpstreamModel)),
			"  "+advertisedRowSummary(row),
			"  Declared protocols (not grants): "+advertisedProtocols(row.SupportedProtocols),
			"  Rates /M tokens · IN "+advertisedRate(row.Prices.Input)+" · OUT "+advertisedRate(row.Prices.Output),
			"  CACHE READ "+advertisedRate(row.Prices.CacheRead)+" · CACHE WRITE "+advertisedRate(row.Prices.CacheWrite),
		)
	}
	return lines
}

func advertisedRowSummary(row litellm.ModelInfoRow) string {
	return "deployment " + strconv.Quote(string(row.Deployment)) + " · IN " + advertisedLimit(row.Limits.InputTokens) +
		" · OUT " + advertisedLimit(row.Limits.OutputTokens) + " · context unknown"
}

func advertisedLimit(tokens int64) string {
	if tokens <= 0 {
		return "unknown"
	}
	return strconv.FormatInt(tokens, 10)
}

func advertisedRate(rate llm.TokenRate) string {
	if !rate.Known {
		return "unknown"
	}
	return strconv.FormatFloat(rate.PerMillionTokens, 'g', -1, 64)
}

func advertisedProtocols(protocols []llm.ModelProtocol) string {
	if len(protocols) == 0 {
		return "unknown / none recognized"
	}
	parts := make([]string, len(protocols))
	for i, protocol := range protocols {
		parts[i] = protocol.String()
	}
	return strings.Join(parts, ", ")
}

func (d *targetModelInfoDialog) draw(scr uv.Screen, area uv.Rectangle) {
	lay, ok := drawDialogPane(scr, area, "gateway advertised", min(110, area.Dx()-2), min(30, area.Dy()-2), palette.Border, palette.Primary)
	if !ok {
		return
	}
	lines := d.lines() // Cached reads only; never refresh on render.
	d.viewport = lay.Body.Dy()
	d.scroll = min(max(0, d.scroll), max(0, len(lines)-d.viewport))
	drawPaddedLine(scr, lay.Context, "Prepared connection snapshot · "+d.target.Spec.Name)
	for i, line := range lines[d.scroll:min(len(lines), d.scroll+d.viewport)] {
		drawPaddedLine(scr, uv.Rect(lay.Body.Min.X, lay.Body.Min.Y+i, lay.Body.Dx(), 1), line)
	}
	drawPaddedLine(scr, lay.Footer, keyLegend(keyHint{"r", "refresh optional metadata"}, keyHint{"↑↓", "scroll"}, keyHint{"esc", "close"}))
}
