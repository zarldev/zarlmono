package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// InspectionCommandForTest replaces only the pending command's loader so tests
// can control completion while exercising production ownership and routing.
func InspectionCommandForTest(m *UI, load func(context.Context) InspectorSnapshot) tea.Cmd {
	return m.inspectionOperation.command(load)
}

// InspectionPendingForTest reports whether the UI still owns an inspection.
func InspectionPendingForTest(m *UI) bool { return m.inspectionOperation != nil }

// InspectionPromptLinesForTest returns the actual cached prompt layout.
func InspectionPromptLinesForTest(m *UI, width int) []string {
	if m.overlay.active() {
		return m.overlay.top().(*inspector).contentLines(width)
	}
	return m.activeContextViewLines(width)
}

// InspectionUncachedPromptLinesForTest measures the former per-frame formatter
// using the identical loaded snapshot, without the new layout cache.
func InspectionUncachedPromptLinesForTest(m *UI, width int) []string {
	return m.overlay.top().(*inspector).promptLines(width)
}
