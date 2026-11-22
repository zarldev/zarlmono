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

func (m *UI) compactNowCmd() tea.Cmd { return m.startManualCompaction() }

func (m *UI) applyCompactNowFinished(msg compactNowFinishedMsg) {
	m.session.logEvent("manual compact", fmt.Sprintf("%d→%d msgs engine=%s", msg.Before, msg.After, msg.Engine))
	if msg.Before == msg.After && msg.BytesTrimmed == 0 {
		m.session.SetToast("nothing to compact")
		return
	}
	m.session.SetToastTone(compactionNotice(msg.Before, msg.After, msg.BytesTrimmed, msg.Engine), toastInfo)
}
