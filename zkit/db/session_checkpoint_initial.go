package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// SaveInitialSessionCheckpoint atomically establishes an exact initial source
// head, recovery draft, empty canonical transcript, BEFORE checkpoint and active
// pointer. expectedActive is retained for API compatibility but is ignored:
// another conversation's selection does not invalidate this source. Only new
// sessions or legacy empty drafts can be promoted. expected must be the session
// content version observed before preparing the promotion. The caller validates
// the opaque context envelope and checkpoint semantics; historical context is
// never upgraded by this operation.
func (s *Store) SaveInitialSessionCheckpoint(ctx context.Context, record SessionRecord, checkpoint SessionCheckpoint, expectedActive string, expected SessionContentVersion) error {
	checkpoint.CreatedAt = time.Now().Truncate(time.Millisecond)
	checkpoint.Checksum = checkpointChecksum(checkpoint)
	return s.WithTx(ctx, func(tx *Store) error {
		if err := tx.establishInitialCheckpointSource(ctx, record, checkpoint, expectedActive, expected); err != nil {
			return err
		}
		return tx.saveSessionCheckpoint(ctx, checkpoint)
	})
}

// establishInitialCheckpointSource requires a transaction and leaves publication
// to its caller so history batches and references can join the same commit.
func (s *Store) establishInitialCheckpointSource(ctx context.Context, record SessionRecord, checkpoint SessionCheckpoint, _ string, expected SessionContentVersion) error {
	if len(checkpoint.Payload) > CheckpointPayloadLimit {
		return ErrCheckpointQuota
	}
	if record.ID == "" || record.Workspace == "" || record.MessageCount != 0 || !json.Valid(record.ContextJSON) ||
		!checkpointMetadataValid(checkpoint) || checkpoint.SessionID != record.ID || checkpoint.SourceSessionID != record.ID ||
		checkpoint.Workspace != record.Workspace || checkpoint.SourceRevision != 0 {
		return ErrCheckpointCorrupt
	}
	tx := s
	if err := tx.checkSessionVersion(ctx, record.ID, expected); err != nil {
		return err
	}
	if _, err := tx.getSessionTranscript(ctx, record.ID); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return ErrCheckpointConflict
	}
	existing, err := tx.GetSession(ctx, record.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	exists := err == nil
	if exists && (existing.Workspace != record.Workspace || existing.MessageCount != 0 || string(existing.ContextJSON) != "[]") {
		return ErrCheckpointUnavailable
	}
	if err := tx.SaveSession(ctx, record); err != nil {
		return err
	}
	now := checkpoint.CreatedAt.UnixMilli()
	if err := tx.q.EnsureSessionTranscript(ctx, gen.EnsureSessionTranscriptParams{
		SessionID: record.ID, Checksum: transcriptChecksum(record.ID, 0, nil),
		FormatVersion: int64(SessionTranscriptFormatVersion), CreatedAtMs: now, UpdatedAtMs: now,
	}); err != nil {
		return fmt.Errorf("establish initial checkpoint source: %w", err)
	}
	if err := tx.SetSetting(ctx, record.Workspace, activeSessionSettingKey, record.ID); err != nil {
		return err
	}
	return nil
}
