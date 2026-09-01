package tui

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

var errNothingToCopy = errors.New("nothing to copy")

type timelineItem = item

type clipboardWriteResultMsg struct {
	label string
	err   error
}

func clipboardWriteCmd(label, text string) tea.Cmd {
	text = strings.TrimSpace(text)
	if text == "" {
		return func() tea.Msg { return clipboardWriteResultMsg{label: label, err: errNothingToCopy} }
	}
	return func() tea.Msg {
		return clipboardWriteResultMsg{label: label, err: clipboard.WriteAll(text)}
	}
}

func (m *UI) applyClipboardWriteResult(msg clipboardWriteResultMsg) tea.Cmd {
	if msg.err != nil {
		m.session.SetToast("copy: " + msg.err.Error())
	} else {
		m.session.SetToast(msg.label + " copied")
	}
	return m.toastExpiryCmd()
}

func (m *UI) copyLastAssistantResponse() tea.Cmd {
	return clipboardWriteCmd("last response", m.LatestAssistantResponse())
}

// LatestAssistantResponse returns the newest non-empty assistant response,
// including responses rendered inside nested sub-agent and tool groups.
func (m *UI) LatestAssistantResponse() string {
	return lastAssistantResponse(m.timeline.items)
}

func lastAssistantResponse(items []timelineItem) string {
	for index := len(items) - 1; index >= 0; index-- {
		if response := lastAssistantResponseInItem(items[index]); response != "" {
			return response
		}
	}
	return ""
}

func lastAssistantResponseInItem(it item) string {
	switch value := it.(type) {
	case *assistantItem:
		if strings.TrimSpace(value.content) != "" {
			return value.content
		}
	case *subAgentItem:
		return lastAssistantResponse(value.children)
	case *groupItem:
		return lastAssistantResponse(value.children)
	case *toolItem:
		return lastAssistantResponse(value.childItems())
	}
	return ""
}
