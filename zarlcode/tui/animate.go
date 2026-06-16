package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// frameMsg drives the cockpit's streaming animation. A single self-sustaining
// tick loop runs only while a turn is in flight; it stops scheduling itself
// the moment the run ends, so an idle TUI does no work.
type frameMsg struct{}

// frameInterval is the animation cadence. ~8fps is smooth enough for the
// gauge pulse and braille activity spinner without spending cycles redrawing a
// mostly static pane.
const frameInterval = 120 * time.Millisecond

// tick schedules the next animation frame.
func tick() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return frameMsg{} })
}
