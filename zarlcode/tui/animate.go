package tui

import (
	"sync/atomic"
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

// oneShotTimerCmd returns a timer command that can only start once. Bubble Tea
// may invoke the same command closure more than once while dispatching commands;
// tea.Tick is not reusable and blocks forever on its drained timer when that
// happens. Claiming the command before creating its timer prevents both the
// blocked goroutine and duplicate tick loops.
func oneShotTimerCmd(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	var started atomic.Bool
	return func() tea.Msg {
		if started.Swap(true) {
			return nil
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		return fn(<-timer.C)
	}
}

// tick schedules the next animation frame.
func tick() tea.Cmd {
	return oneShotTimerCmd(frameInterval, func(time.Time) tea.Msg { return frameMsg{} })
}
