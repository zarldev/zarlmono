package tui

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/db"
)

// executeVersionedSessionPersist publishes only a successful transaction's
// receipt. Never reread after commit: that could adopt a competing writer.
func executeVersionedSessionPersist(ctx context.Context, store *db.Store, op *sessionPersistOp) error {
	expected := op.sourceObserved.expected()
	var version db.SessionContentVersion
	var err error
	switch op.kind {
	case sessionPersistRename:
		version, err = store.RenameSessionVersioned(ctx, op.oldID, op.label, expected)
	case sessionPersistDraft:
		version, err = store.SaveSessionDraftVersioned(ctx, op.draft, expected)
	case sessionPersistClearDraft:
		version, err = store.ClearSessionDraftVersioned(ctx, op.oldID, expected)
	case sessionPersistTranscript:
		version, err = store.UpdateActiveTranscriptVersioned(ctx, op.transcript.update, expected)
	case sessionPersistFull:
		snapshot := op.snapshot
		if snapshot.exact {
			version, err = store.CommitCheckpointTurnVersioned(ctx, snapshot.record, snapshot.transcript, expected, snapshot.historyBatches...)
		} else {
			version, err = store.CommitCompletedTurnVersioned(ctx, snapshot.record, snapshot.transcript, expected, snapshot.historyBatches...)
		}
		if err == nil {
			snapshot.acknowledgeHistory()
		}
	}
	if err == nil {
		op.sourceWritten.Store(&sourceWrite{before: expected, after: version})
	}
	return err
}
