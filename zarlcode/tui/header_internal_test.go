package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func TestTimelineTitleStatus_Render(t *testing.T) {
	m := New()
	m.session.Workspace = "~/proj"
	m.session.Branch = "main"
	m.session.Model = "qwen3"
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = mm.(*UI)

	m.composer.insert("/")
	out := ansi.Strip(m.View().Content)
	title := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(title, "follow") {
		t.Fatalf("top row should expose transcript follow state:\n%s", title)
	}
	// The composer already communicates workflow mode, so the header retains only
	// product, model, execution, and viewport state.
	for _, want := range []string{"ƶ", "qwen3", "idle", "follow"} {
		if !strings.Contains(title, want) {
			t.Errorf("timeline header missing %q:\n%s", want, title)
		}
	}
	if strings.Contains(title, "CHAT") {
		t.Errorf("default chat mode should not add header noise:\n%s", title)
	}
	if !strings.HasPrefix(title, "ƶ") {
		t.Errorf("orientation mark should be flush left, got %q", title)
	}
	identity := strings.Index(title, "ƶ")
	run := strings.Index(title, "idle")
	model := strings.Index(title, "qwen3")
	viewport := strings.Index(title, "follow")
	if identity < 0 || run <= identity || model <= run || viewport <= model {
		t.Errorf("header grouping should be identity/run then model/viewport, got %q", title)
	}
	if strings.Contains(title, appDisplayName) {
		t.Errorf("header should not repeat the full product title, got %q", title)
	}
	for _, mode := range []string{"CHAT", "BUILD", "PLAN"} {
		if strings.Contains(title, mode) {
			t.Errorf("composer-owned mode %q should not appear in header:\n%s", mode, title)
		}
	}
	for _, unwanted := range []string{"~/proj", "main", "session"} {
		if strings.Contains(title, unwanted) {
			t.Errorf("timeline title should not include %q:\n%s", unwanted, title)
		}
	}
	if !strings.Contains(out, "slash commands") || !strings.Contains(out, "/clear") || !strings.Contains(out, "/help") {
		t.Fatalf("status should show slash commands while composing '/':\n%s", out)
	}
	for _, unwanted := range []string{"ctrl+p", "ctrl+s", "ctrl+t", "ctrl+l"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("status should not include %q:\n%s", unwanted, out)
		}
	}
}

func TestTimelineHeaderPreservesViewportAtNarrowWidths(t *testing.T) {
	m := New()
	m.session.Model = "an-extremely-long-model-name-that-is-decorative"
	m.session.Run.Running = true
	m.timeline.viewHeight = 2
	for i := range 8 {
		m.timeline.addUser("message " + itoa(i))
	}
	m.timeline.enterBrowse()
	m.timeline.scrollTop = 0

	buf := uv.NewScreenBuffer(36, 2)
	m.drawTimelineTopBar(buf, buf.Bounds())
	line := strings.Split(ansi.Strip(buf.Render()), "\n")[0]
	if !strings.Contains(line, "browse 0%") {
		t.Fatalf("narrow header dropped viewport state: %q", line)
	}
	if strings.Contains(line, "an-extremely-long") {
		t.Fatalf("narrow header should truncate decorative model before viewport: %q", line)
	}
}

func TestStatusHintShowsStopWhileRunning(t *testing.T) {
	m := New()
	if got := m.statusHint(); !strings.Contains(got, "ctrl+c quit") || !strings.Contains(got, "ctrl+q ctx") || strings.Contains(got, "esc stop") || strings.Contains(got, "esc quit") {
		t.Fatalf("idle status hint should show ctrl+c quit and ctrl+q ctx, not esc quit/stop:\n%s", got)
	}
	m.session.Run.Running = true
	if got := m.statusHint(); !strings.Contains(got, "esc stop") || !strings.Contains(got, "ctrl+c quit") || !strings.Contains(got, "ctrl+q ctx") || strings.Contains(got, "esc quit") {
		t.Fatalf("running status hint should show esc stop plus ctrl+c quit and ctrl+q ctx:\n%s", got)
	}
}

func TestHeaderModeBadgeUsesModeAccent(t *testing.T) {
	old := palette
	UseTheme(theme.Theme{
		Assistant: "#111111",
		Tool:      "#222222",
		PlanMode:  "#333333",
		Border:    "#444444",
	})
	t.Cleanup(func() { UseTheme(old) })

	m := New()
	if got := m.headerModeBadge(); !strings.Contains(got, theme.Color("#111111").FG()+"chat") {
		t.Fatalf("chat badge not assistant-themed: %q", got)
	}

	m.session.Run.Running = true
	if got := m.headerModeBadge(); !strings.Contains(got, theme.Color("#222222").FG()+"build") {
		t.Fatalf("build badge not tool-themed: %q", got)
	}

	m.session.PlanMode = true
	if got := m.headerModeBadge(); !strings.Contains(got, theme.Color("#333333").FG()+"plan") {
		t.Fatalf("plan badge not plan-themed: %q", got)
	}
}

func TestShortenHome(t *testing.T) {
	if got := shortenHome("/tmp/nothome"); got != "/tmp/nothome" {
		t.Errorf("non-home path changed: %q", got)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	if got := shortenHome(home); got != "~" {
		t.Errorf("home → %q, want ~", got)
	}
	if got := shortenHome(filepath.Join(home, "proj")); got != "~"+string(os.PathSeparator)+"proj" {
		t.Errorf("home subdir → %q", got)
	}
}

func TestStatusHintListsCurrentShortcuts(t *testing.T) {
	m := New()
	// Compose footer: only the essentials, ctrl+g as gateway.
	got := m.statusHint()
	for _, want := range []string{"enter submit", "shift+enter newline", "tab browse", "shift+tab plan mode", "ctrl+c quit", "ctrl+q ctx", "ctrl+g"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compose status hint missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"ctrl+s", "ctrl+p", "ctrl+t", "ctrl+l"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("compose status hint should not include %q:\n%s", unwanted, got)
		}
	}

	m.session.PlanMode = true
	got = m.statusHint()
	for _, want := range []string{"enter submit", "shift+tab build", "ctrl+c quit", "ctrl+q ctx", "ctrl+g"} {
		if !strings.Contains(got, want) {
			t.Fatalf("plan status hint missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"ctrl+s", "ctrl+p", "ctrl+t", "ctrl+l"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("plan status hint should not include %q:\n%s", unwanted, got)
		}
	}

	m.session.PlanMode = false
	m.timeline.browsing = true
	for _, want := range []string{"↑↓/jk move", "v select", "pgup/pgdn page", "esc/i compose", "ctrl+g"} {
		if got := m.statusHint(); !strings.Contains(got, want) {
			t.Fatalf("browse status hint missing %q:\n%s", want, got)
		}
	}
	m.timeline.selection = transcriptSelection{active: true}
	for _, want := range []string{"VISUAL", "↑↓/jk extend", "y copy", "esc cancel", "ctrl+c quit"} {
		if got := m.statusHint(); !strings.Contains(got, want) {
			t.Fatalf("visual selection status hint missing %q:\n%s", want, got)
		}
	}
	m.timeline.selection = transcriptSelection{}

	m.timeline.browsing = false
	m.session.SetCockpitExpanded(true)
	for _, want := range []string{"ctrl+l / esc / q close", "ctrl+g"} {
		if got := m.statusHint(); !strings.Contains(got, want) {
			t.Fatalf("context status hint missing %q:\n%s", want, got)
		}
	}
}
