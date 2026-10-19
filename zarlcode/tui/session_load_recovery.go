package tui

import (
	"context"

	"github.com/zarldev/zarlmono/zarlcode/rewind"
	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/db"
)

// persistTranscriptRecovery keeps an exact resume head paired with the
// recovered canonical transcript. Legacy session context does not claim an
// exact revision and continues to use the transcript-only recovery write.
func persistTranscriptRecovery(
	ctx context.Context,
	store *db.Store,
	record db.SessionRecord,
	expectedRevision uint64,
	thread transcript.Thread,
	exactHead *rewind.ResumeState,
	expectedVersion db.SessionContentVersion,
) (db.SessionContentVersion, error) {
	update, err := transcriptRecoveryUpdate(record, expectedRevision, thread)
	if err != nil {
		return db.SessionContentVersion{}, err
	}
	if exactHead == nil {
		return store.UpdateActiveTranscriptVersioned(ctx, update, expectedVersion)
	}

	contextJSON, err := rewind.EncodeResume(
		thread.Revision(),
		exactHead.Context,
		exactHead.Target,
		exactHead.SettledTurnID,
		exactHead.EventWatermark,
		exactHead.InitialContinuation,
	)
	if err != nil {
		return db.SessionContentVersion{}, err
	}
	record.ContextJSON = contextJSON
	version, err := store.CommitCheckpointTurnVersioned(ctx, record, update, expectedVersion)
	if err != nil {
		return db.SessionContentVersion{}, err
	}
	exactHead.Revision = thread.Revision()
	return version, nil
}
