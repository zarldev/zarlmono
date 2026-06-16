package tui

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// Pane is a rectangular region of the screen that handles its own rendering
// and event dispatch. A pane returns nil from Update when a message isn't
// for it, letting the shell try the next pane in priority order.
//
// Panes do not implement tea.Model. They are driven imperatively by the
// shell: the shell computes the pane's bounds from the layout, calls Draw
// with that rectangle, and routes messages through Update.
type Pane interface {
	// Draw paints the pane's content into scr within area. The shell
	// guarantees area is non-empty and clipped to the screen.
	Draw(scr uv.Screen, area uv.Rectangle)

	// Update handles a message intended for this pane. Returns a command
	// the shell should execute, or nil when the message was not consumed.
	Update(msg tea.Msg) tea.Cmd
}

// Focusable is a Pane that can receive keyboard focus. The shell tracks
// which pane is focused and routes KeyPressMsg, PasteMsg, and ClipboardMsg
// to it first (before falling back to broadcast).
//
// Focus/Blur are called by the shell during focus transitions. Panes use
// Focused() to decide whether to render a cursor, selection highlight, or
// other focus-dependent chrome.
type Focusable interface {
	Pane

	// Focused reports whether this pane currently holds keyboard focus.
	Focused() bool

	// Focus is called when the pane gains keyboard focus.
	Focus()

	// Blur is called when the pane loses keyboard focus.
	Blur()
}
