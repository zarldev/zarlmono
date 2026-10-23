package tui_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zarlcode/tui/teasink"
)

func TestBeforeCheckpointHelloThroughEventLoop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newBeforeFixture(t)
	settings := engine.NewSettings(f.store, nil, nil, f.ws.Root())
	settings.Registry = nil // Root inference uses the fixture provider only.
	for key, value := range map[string]string{
		prefs.KeyProgramOutputRecovery: "on", prefs.KeyCompactEngine: "tiered", prefs.KeyCompactProvider: "(active)",
		prefs.KeySpawnDefaultImplementProvider: "openai-codex", prefs.KeySpawnDefaultImplementModel: "fixture-child",
	} {
		if err := settings.Svc.SetSetting(t.Context(), prefs.ScopeWorkspace, key, value); err != nil {
			t.Fatal(err)
		}
	}
	engine.WithSettings(settings)(f.live)
	// Configured default targets and a depth above one previously recursed
	// forever before the first ConversationStarted event.
	f.live.SetLimits(0, 8, 4, 4)
	f.ui.SetSettings(settings)
	f.ui.SetStartupReady(true)
	called := make(chan struct{})
	f.provider.check = func(context.Context) { close(called) }
	ended := make(chan struct{}, 1)
	program := tea.NewProgram(f.ui, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	f.sink.SetSend(func(msg tea.Msg) {
		program.Send(msg)
		if _, ok := msg.(teasink.ConversationEndedMsg); ok {
			ended <- struct{}{}
		}
	})
	done := make(chan struct{})
	var runErr error
	go func() { defer close(done); _, runErr = program.Run() }()
	defer func() { program.Kill(); <-done }()
	program.Send(tea.WindowSizeMsg{Width: 100, Height: 40})
	program.Send(tea.KeyPressMsg{Code: 'h', Text: "hello"})
	program.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("hello never reached the fake provider through the BEFORE barrier")
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("hello completion never reached the event sink")
	}
	program.Quit()
	<-done
	if runErr != nil {
		t.Fatal(runErr)
	}
	if rendered := strings.Join(f.ui.RenderTranscript(100, 40), "\n"); !strings.Contains(rendered, "answer") {
		t.Fatalf("hello answer not rendered: %s", rendered)
	}
}
