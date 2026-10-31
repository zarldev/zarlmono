package tui_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zarlcode/tui"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
	"github.com/zarldev/zarlmono/zkit/tui/theme"
)

func inspectionUI(t *testing.T, contextView bool) *tui.UI {
	t.Helper()
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	if contextView {
		ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'l'})
		ui.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		ui.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	} else {
		ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
		ui.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	t.Cleanup(func() {
		if err := ui.FlushSessionPersistence(context.Background()); err != nil {
			t.Errorf("join inspection: %v", err)
		}
	})
	return ui
}

func TestInspectionLoadsOutsideUpdateAndReusesPromptLayout(t *testing.T) {
	for _, contextView := range []bool{false, true} {
		name := "inspector"
		if contextView {
			name = "context"
		}
		t.Run(name, func(t *testing.T) {
			ui := inspectionUI(t, contextView)
			if out := ansi.Strip(ui.View().Content); !strings.Contains(out, "loading inspection") {
				t.Fatalf("first frame must not wait for inspection: %s", out)
			}
			load := tui.InspectionCommandForTest(ui, func(context.Context) tui.InspectorSnapshot {
				return tui.InspectorSnapshot{PromptSystem: strings.Repeat("prompt-marker\n", 1000)}
			})
			ui.Update(load())
			if tui.InspectionPendingForTest(ui) {
				t.Fatal("completed inspection still pending")
			}
			lines := tui.InspectionPromptLinesForTest(ui, 100)
			if !strings.Contains(strings.Join(lines, "\n"), "prompt-marker") {
				t.Fatal("loaded prompt is absent")
			}
			for range 5 {
				ui.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
				ui.View()
			}
			if tui.InspectionPendingForTest(ui) {
				t.Fatal("scroll/redraw scheduled another inspection")
			}
			// View uses a different width; establish the same key twice.
			lines = tui.InspectionPromptLinesForTest(ui, 100)
			cached := tui.InspectionPromptLinesForTest(ui, 100)
			if &lines[0] != &cached[0] {
				t.Fatal("unchanged prompt layout was rebuilt")
			}
			resized := tui.InspectionPromptLinesForTest(ui, 50)
			if &resized[0] == &cached[0] {
				t.Fatal("resize retained old layout")
			}
			tui.UseTheme(theme.Theme{})
			rethemed := tui.InspectionPromptLinesForTest(ui, 50)
			if &rethemed[0] == &resized[0] {
				t.Fatal("theme change retained old layout")
			}
			ui.Update(tea.KeyPressMsg{Code: 'r'})
			if !tui.InspectionPendingForTest(ui) || !strings.Contains(ansi.Strip(ui.View().Content), "loading inspection") {
				t.Fatal("explicit refresh did not request a new snapshot")
			}
		})
	}
}

func TestInspectionCloseRejectsLateResults(t *testing.T) {
	ui := inspectionUI(t, false)
	old := tui.InspectionCommandForTest(ui, func(context.Context) tui.InspectorSnapshot {
		return tui.InspectorSnapshot{PromptSystem: "obsolete prompt"}
	})
	result := old()
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
	ui.Update(result)
	if !strings.Contains(ansi.Strip(ui.View().Content), "loading inspection") {
		t.Fatal("late result replaced reopened inspector")
	}
}

func TestInspectionShutdownClaimsUnstartedCommand(t *testing.T) {
	ui := inspectionUI(t, false)
	cmd := tui.InspectionCommandForTest(ui, func(context.Context) tui.InspectorSnapshot {
		t.Fatal("loader ran after shutdown")
		return tui.InspectorSnapshot{}
	})
	if err := ui.FlushSessionPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if cmd() != nil {
		t.Fatal("unstarted command produced a result after shutdown")
	}
}

func TestInspectionCancellationRetainsJoinOwnership(t *testing.T) {
	ui := inspectionUI(t, false)
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cmd := tui.InspectionCommandForTest(ui, func(ctx context.Context) tui.InspectorSnapshot {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return tui.InspectorSnapshot{PromptSystem: "canceled prompt"}
	})
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	// Always release and join the owned worker, including on assertion failure.
	defer func() { close(release); <-result }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("inspection did not start")
	}
	ui.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("closing inspector did not cancel its command")
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("join deadline")
	cancel(cause)
	if err := ui.FlushSessionPersistence(ctx); !errors.Is(err, cause) {
		t.Fatalf("shutdown error = %v, want cancellation cause", err)
	}
	if !tui.InspectionPendingForTest(ui) {
		t.Fatal("timed out shutdown abandoned command ownership")
	}
}

func TestContextEventsDoNotRequestInspection(t *testing.T) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'l'})
	// Previous tab from overview is events; no prompt or tool tab visited.
	ui.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	ui.View()
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if tui.InspectionPendingForTest(ui) {
		t.Fatal("event-only view assembled a prompt")
	}
}

func TestInspectionRefreshesAfterModeChange(t *testing.T) {
	ui := inspectionUI(t, true)
	load := tui.InspectionCommandForTest(ui, func(context.Context) tui.InspectorSnapshot {
		return tui.InspectorSnapshot{PromptSystem: "build-only marker"}
	})
	ui.Update(load())
	ui.SetPlanMode(true)
	ui.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if !tui.InspectionPendingForTest(ui) || strings.Contains(ansi.Strip(ui.View().Content), "build-only marker") {
		t.Fatal("mode change retained previous prompt snapshot")
	}
}

func TestInspectionRefreshClampsProcessSelection(t *testing.T) {
	t.Setenv("ZARLCODE_ASCII", "")
	ui := inspectionUI(t, false)
	load := tui.InspectionCommandForTest(ui, func(context.Context) tui.InspectorSnapshot {
		return tui.InspectorSnapshot{Processes: []code.ProcessInfo{{ID: "one"}, {ID: "two"}}}
	})
	ui.Update(load())
	ui.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // runtime
	ui.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // processes
	ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	ui.Update(tea.KeyPressMsg{Code: 'r'})
	load = tui.InspectionCommandForTest(ui, func(context.Context) tui.InspectorSnapshot {
		return tui.InspectorSnapshot{Processes: []code.ProcessInfo{{ID: "one"}}}
	})
	ui.Update(load())
	if !strings.Contains(ansi.Strip(ui.View().Content), "▸ one") {
		t.Fatal("refreshed process selection is not visible")
	}
}

func TestInspectionJoinDoesNotPreemptDraftSave(t *testing.T) {
	f := newBeforeFixture(t)
	f.ui.SetWorkspace(f.ws.Root(), "saved-model")
	f.ui.Update(tea.PasteMsg{Content: "durable before inspection join"})
	f.ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
	started, release := make(chan struct{}), make(chan struct{})
	cmd := tui.InspectionCommandForTest(f.ui, func(ctx context.Context) tui.InspectorSnapshot {
		close(started)
		<-ctx.Done()
		<-release
		return tui.InspectorSnapshot{}
	})
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	defer func() {
		close(release)
		<-done
		if err := f.ui.FlushSessionPersistence(context.Background()); err != nil {
			t.Errorf("final join: %v", err)
		}
	}()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := f.ui.FlushSessionPersistence(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("join error = %v", err)
	}
	record, err := f.store.GetSession(t.Context(), f.ui.SessionIdentity())
	if err != nil || !strings.Contains(string(record.PendingJSON), "durable before inspection join") {
		t.Fatalf("inspection join preempted draft save: %v", err)
	}
}

func BenchmarkInspectionPromptRedraw(b *testing.B) {
	ui := tui.New()
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 32})
	ui.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'o'})
	ui.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	load := tui.InspectionCommandForTest(ui, func(context.Context) tui.InspectorSnapshot {
		return tui.InspectorSnapshot{PromptSystem: strings.Repeat("representative long prompt line\n", 10000)}
	})
	ui.Update(load())
	tui.InspectionPromptLinesForTest(ui, 100)
	b.Run("uncached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			tui.InspectionUncachedPromptLinesForTest(ui, 100)
		}
	})
	b.Run("cached", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			tui.InspectionPromptLinesForTest(ui, 100)
		}
	})
}
