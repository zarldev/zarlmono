package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRewindPreviewNarrowWarningAndReview(t *testing.T) {
	f := newBeforeFixture(t)
	f.provider.check = func(context.Context) {}
	settleRewindTurn(t, f, "selected prompt")
	settleRewindTurn(t, f, "later prompt")
	sourceID, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil {
		t.Fatal(err)
	}
	previewRewindPrompt(f, 2)
	f.ui.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	view := f.ui.View().Content
	if !strings.Contains(view, "Files were not restored") {
		t.Fatalf("narrow preview hides files warning:\n%s", view)
	}
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	active, err := f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || active != sourceID {
		t.Fatal("preview applied before reviewing clipped consequences", err)
	}

	var screens strings.Builder
	for range 80 {
		for _, line := range strings.Split(ansi.Strip(f.ui.View().Content), "\n") {
			screens.WriteString(strings.Trim(line, " │"))
			screens.WriteByte(' ')
		}
		f.ui.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	for _, text := range []string{"not undone", "Recheck", "existing draft", "not submitted", "Later prompts", "selected prompt"} {
		if !strings.Contains(screens.String(), text) {
			t.Errorf("cannot read %q in narrow preview", text)
		}
	}
	f.ui.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	active, err = f.store.GetSettingExact(t.Context(), f.ws.Root(), "active_session")
	if err != nil || active != sourceID {
		t.Fatal("cancel changed source", err)
	}
}
