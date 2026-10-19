package tui

import (
	"fmt"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
)

func (tl *timeline) addControl(turnID string, control runner.ReplayControl) {
	// Retain audit records without projecting them into the chat timeline.
	tl.applyTranscript(transcript.ControlRecorded{TurnID: turnID, Control: control})
}

// reconcileControls projects durable replay observations that were committed
// before a crash but did not reach the canonical human transcript. Restored
// observations are appended in durable replay order; recovery does not claim to
// reconstruct their original position in transcript chronology. Existing
// identities retain their stored attribution and validate payload idempotently.
func reconcileControls(thread transcript.Thread, controls []runner.ReplayControl) (transcript.Thread, error) {
	if len(controls) == 0 {
		return thread, nil
	}
	existingTurns := make(map[string]string)
	for _, entry := range thread.Entries() {
		if entry.Kind == transcript.EntryKinds.ENTRYCONTROLCHANGE {
			existingTurns[entry.ID] = entry.TurnID
		}
	}
	reducer := transcript.NewReducerFrom(thread)
	for _, control := range controls {
		turnID, exists := existingTurns[control.ID]
		if !exists {
			turnID = control.TurnID
			if turnID == "" {
				turnID = "recovered-control:" + control.ID
			}
		}
		if _, err := reducer.Apply(transcript.ControlRecorded{TurnID: turnID, Control: control}); err != nil {
			return transcript.Thread{}, fmt.Errorf("reconcile request control %q: %w", control.ID, err)
		}
	}
	return reducer.Thread(), nil
}
