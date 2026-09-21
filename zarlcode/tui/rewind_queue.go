package tui

import "github.com/zarldev/zarlmono/zarlcode/transcript"

// addQueueIntent records queueing, not delivery. The actual user message is
// appended only at injection or top-level start. A checkpoint may retain this
// truthful historical notice without claiming pending input reached the model.
// Legacy ENTRYQUEUEDUSER records remain ineligible until explicitly delivered.
func (tl *timeline) addQueueIntent(text string) {
	tl.applyTranscript(transcript.NoticeAdded{Text: "Queued input (not yet delivered): " + text})
	q := &queuedUserItem{text: text}
	tl.appendItem(q)
	tl.queued = append(tl.queued, q)
	tl.queuedEntries = append(tl.queuedEntries, "")
}

func (tl *timeline) removeQueueIntent() {
	if len(tl.queued) == 0 || tl.queuedEntries[0] != "" {
		return
	}
	q := tl.queued[0]
	tl.queued = tl.queued[1:]
	tl.queuedEntries = tl.queuedEntries[1:]
	for i, it := range tl.items {
		if it == q {
			items := append([]item(nil), tl.items[:i]...)
			items = append(items, tl.items[i+1:]...)
			tl.reorderItems(items, i)
			break
		}
	}
}
