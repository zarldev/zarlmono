package tui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
)

type compactNowFinishedMsg struct {
	Before       int
	After        int
	BytesTrimmed int
	Engine       string
	Duration     time.Duration
}

type compactNowFailedMsg struct{ Error string }

func (m *UI) compactNowCmd() tea.Cmd {
	if m.live == nil {
		return func() tea.Msg { return compactNowFailedMsg{Error: "live runner is not available"} }
	}
	return func() tea.Msg {
		started := time.Now()
		res, err := m.live.CompactNow(m.appContext())
		if err != nil {
			return compactNowFailedMsg{Error: err.Error()}
		}
		return compactNowFinishedMsg{
			Before:       res.MessagesBefore,
			After:        res.MessagesAfter,
			BytesTrimmed: res.BytesTrimmed,
			Engine:       res.Engine,
			Duration:     time.Since(started),
		}
	}
}

func (m *UI) applyCompactNowFinished(msg compactNowFinishedMsg) {
	m.session.logEvent("manual compact", fmt.Sprintf("%d→%d msgs engine=%s", msg.Before, msg.After, msg.Engine))
	if msg.Before == msg.After && msg.BytesTrimmed == 0 {
		m.session.SetToast("nothing to compact")
		return
	}
	m.session.SetToast(fmt.Sprintf("compacted %d→%d messages", msg.Before, msg.After))
}
