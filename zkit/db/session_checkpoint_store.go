package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// CheckpointPayloadLimit bounds checkpoint metadata and legacy envelopes, not
// canonical history. Reference checkpoints store their history separately.
const CheckpointPayloadLimit = 1 << 20

// CheckpointSessionBudget bounds retained checkpoint envelopes only. Eviction
// never deletes immutable canonical values or reachable branch prefixes.
const CheckpointSessionBudget = 8 << 20

// CheckpointRetention is the inactivity period before unpinned checkpoints expire.
const CheckpointRetention = 30 * 24 * time.Hour

// ErrCheckpointUnavailable means a checkpoint is absent, expired, or evicted.
var ErrCheckpointUnavailable = errors.New("checkpoint unavailable")

// ErrCheckpointCorrupt means checkpoint integrity or storage metadata is invalid.
var ErrCheckpointCorrupt = errors.New("checkpoint corrupted")

// ErrCheckpointConflict means the source, active session, or destination changed.
var ErrCheckpointConflict = errors.New("checkpoint state conflict")

// ErrCheckpointQuota means exact capture cannot fit without discarding pinned data.
var ErrCheckpointQuota = errors.New("checkpoint storage limit exceeded")

// SessionCheckpoint owns an opaque application-versioned snapshot. The caller
// validates its semantic contents (including credentials exclusion and exact
// context/transcript shape); the store verifies integrity and retention only.
// SessionID is the owner; SourceSessionID remains historical after copying into
// a child. Payload is independently owned on reads and borrowed during writes.
// Checksum and CreatedAt are assigned by SaveSessionCheckpoint.
type SessionCheckpoint struct {
	SessionID       string
	ID              string
	SourceSessionID string
	Workspace       string
	SourceRevision  uint64
	BoundaryID      string
	FormatVersion   uint64
	Payload         []byte
	Checksum        string
	CreatedAt       time.Time
	Pinned          bool
	// History references immutable canonical data; zero denotes a legacy snapshot.
	History CheckpointHistory
}

// CheckpointSummary omits private payloads. It is discovery metadata, not proof
// of rewind eligibility: load and semantically validate the payload before use.
type CheckpointSummary struct {
	SessionID       string
	ID              string
	SourceSessionID string
	Workspace       string
	SourceRevision  uint64
	BoundaryID      string
	FormatVersion   uint64
	Checksum        string
	CreatedAt       time.Time
	Pinned          bool
	PayloadBytes    int64
}

// SaveSessionCheckpoint atomically records a new exact BEFORE snapshot at the
// current durable source revision. The source must exist in the given workspace.
// Initial revision zero creates an empty canonical transcript if absent.
// Duplicate identities conflict; snapshots are never overwritten. Quota eviction
// and session activity refresh roll back if the insert cannot commit.
func (s *Store) SaveSessionCheckpoint(ctx context.Context, checkpoint SessionCheckpoint) error {
	if len(checkpoint.Payload) > CheckpointPayloadLimit {
		return ErrCheckpointQuota
	}
	if !checkpointMetadataValid(checkpoint) || checkpoint.SourceSessionID != checkpoint.SessionID {
		return ErrCheckpointCorrupt
	}
	checkpoint.CreatedAt = time.Now().Truncate(time.Millisecond)
	checkpoint.Checksum = checkpointChecksum(checkpoint)
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.saveSessionCheckpoint(ctx, checkpoint)
	})
}

// saveSessionCheckpoint requires a transaction-bound store.
func (s *Store) saveSessionCheckpoint(ctx context.Context, checkpoint SessionCheckpoint) error {
	if err := s.checkCheckpointSource(ctx, checkpoint.SessionID, checkpoint.Workspace, checkpoint.SourceRevision); err != nil {
		return err
	}
	if _, err := s.q.GetSessionCheckpoint(ctx, gen.GetSessionCheckpointParams{SessionID: checkpoint.SessionID, CheckpointID: checkpoint.ID}); err == nil {
		return ErrCheckpointConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check checkpoint identity: %w", err)
	}
	if _, err := s.ExpireSessionCheckpoints(ctx, checkpoint.CreatedAt); err != nil {
		return err
	}
	if err := s.q.TouchCheckpointSession(ctx, gen.TouchCheckpointSessionParams{ID: checkpoint.SessionID, UpdatedAt: checkpoint.CreatedAt.Unix()}); err != nil {
		return fmt.Errorf("refresh checkpoint activity: %w", err)
	}
	if err := s.makeCheckpointRoom(ctx, checkpoint.SessionID, len(checkpoint.Payload)); err != nil {
		return err
	}
	if err := s.q.EnsureSessionTranscript(ctx, gen.EnsureSessionTranscriptParams{
		SessionID: checkpoint.SessionID, Checksum: transcriptChecksum(checkpoint.SessionID, 0, nil),
		FormatVersion: int64(SessionTranscriptFormatVersion), CreatedAtMs: checkpoint.CreatedAt.UnixMilli(), UpdatedAtMs: checkpoint.CreatedAt.UnixMilli(),
	}); err != nil {
		return fmt.Errorf("ensure checkpoint transcript: %w", err)
	}
	return s.insertCheckpoint(ctx, checkpoint)
}

// checkCheckpointSourceSelection is reserved for explicit selection transitions.
// Ordinary saves validate their own source, not the workspace's resume preference.
func (s *Store) checkCheckpointSourceSelection(ctx context.Context, sessionID, workspace string, revision uint64, expectedActive string) error {
	if err := s.checkCheckpointSource(ctx, sessionID, workspace, revision); err != nil {
		return err
	}
	active, err := s.q.GetSetting(ctx, gen.GetSettingParams{Workspace: workspace, Key: activeSessionSettingKey})
	if errors.Is(err, sql.ErrNoRows) && expectedActive == "" {
		err = nil
	} else if errors.Is(err, sql.ErrNoRows) {
		return ErrCheckpointConflict
	}
	if err != nil {
		return fmt.Errorf("read checkpoint active session: %w", err)
	}
	if active != expectedActive {
		return ErrCheckpointConflict
	}
	return nil
}

func (s *Store) checkCheckpointSource(ctx context.Context, sessionID, workspace string, revision uint64) error {
	activity, err := s.q.SessionCheckpointActivity(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCheckpointConflict
	}
	if err != nil {
		return fmt.Errorf("read checkpoint source: %w", err)
	}
	if activity.Workspace != workspace {
		return ErrCheckpointConflict
	}
	transcript, err := s.getSessionTranscript(ctx, sessionID)
	if errors.Is(err, ErrNotFound) && revision == 0 {
		initial, err := s.q.GetSession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("read initial checkpoint source: %w", err)
		}
		if initial.ContextJson != "[]" || initial.MessageCount != 0 {
			return ErrCheckpointUnavailable
		}
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return ErrCheckpointConflict
	}
	if err != nil {
		return err
	}
	if transcript.Revision != revision {
		return ErrCheckpointConflict
	}
	if transcript.FormatVersion != SessionTranscriptFormatVersion {
		return ErrCheckpointCorrupt
	}
	return nil
}

func (s *Store) insertCheckpoint(ctx context.Context, checkpoint SessionCheckpoint) error {
	revision, err := sqliteInteger("checkpoint revision", checkpoint.SourceRevision)
	if err != nil {
		return ErrCheckpointCorrupt
	}
	version, err := sqliteInteger("checkpoint format", checkpoint.FormatVersion)
	if err != nil {
		return ErrCheckpointCorrupt
	}
	if err := s.q.InsertSessionCheckpoint(ctx, gen.InsertSessionCheckpointParams{
		SessionID: checkpoint.SessionID, CheckpointID: checkpoint.ID, SourceSessionID: checkpoint.SourceSessionID,
		Workspace: checkpoint.Workspace, SourceRevision: revision, BoundaryID: checkpoint.BoundaryID,
		FormatVersion: version, Payload: checkpoint.Payload, Checksum: checkpoint.Checksum,
		CreatedAtMs: checkpoint.CreatedAt.UnixMilli(), Pinned: boolToInt64(checkpoint.Pinned),
	}); err != nil {
		return fmt.Errorf("insert checkpoint: %w", err)
	}
	return s.saveCheckpointHistory(ctx, checkpoint)
}

func (s *Store) makeCheckpointRoom(ctx context.Context, sessionID string, incoming int) error {
	rows, err := s.q.ListSessionCheckpoints(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("read checkpoint budget: %w", err)
	}
	total := int64(incoming)
	for _, row := range rows {
		if !row.PayloadBytes.Valid || row.PayloadBytes.Int64 <= 0 || row.PayloadBytes.Int64 > CheckpointPayloadLimit {
			return ErrCheckpointCorrupt
		}
		total += row.PayloadBytes.Int64
	}
	for _, row := range rows {
		if total <= CheckpointSessionBudget {
			return nil
		}
		if row.Pinned != 0 {
			continue
		}
		if err := s.q.DeleteSessionCheckpoint(ctx, gen.DeleteSessionCheckpointParams{SessionID: sessionID, CheckpointID: row.CheckpointID}); err != nil {
			return fmt.Errorf("evict checkpoint: %w", err)
		}
		total -= row.PayloadBytes.Int64
	}
	if total > CheckpointSessionBudget {
		return ErrCheckpointQuota
	}
	return nil
}

// GetSessionCheckpoint returns an independently owned, integrity-checked payload.
// Unsupported application formats must still be rejected by the application.
// Expired unpinned rows are unavailable even before physical retention cleanup.
func (s *Store) GetSessionCheckpoint(ctx context.Context, sessionID, checkpointID string) (SessionCheckpoint, error) {
	var result SessionCheckpoint
	err := s.withReadTx(ctx, func(tx *Store) error {
		var err error
		result, err = tx.getSessionCheckpoint(ctx, sessionID, checkpointID, time.Now())
		return err
	})
	return result, err
}

func (s *Store) getSessionCheckpoint(ctx context.Context, sessionID, checkpointID string, now time.Time) (SessionCheckpoint, error) {
	row, err := s.q.GetSessionCheckpoint(ctx, gen.GetSessionCheckpointParams{SessionID: sessionID, CheckpointID: checkpointID})
	if errors.Is(err, sql.ErrNoRows) {
		return SessionCheckpoint{}, ErrCheckpointUnavailable
	}
	if err != nil {
		return SessionCheckpoint{}, fmt.Errorf("read checkpoint: %w", err)
	}
	activity, err := s.q.SessionCheckpointActivity(ctx, sessionID)
	if err != nil {
		return SessionCheckpoint{}, fmt.Errorf("read checkpoint activity: %w", err)
	}
	if row.Pinned == 0 && activity.UpdatedAt <= now.Add(-CheckpointRetention).Unix() {
		return SessionCheckpoint{}, ErrCheckpointUnavailable
	}
	if row.SourceRevision < 0 || row.FormatVersion <= 0 {
		return SessionCheckpoint{}, ErrCheckpointCorrupt
	}
	checkpoint := SessionCheckpoint{
		SessionID: row.SessionID, ID: row.CheckpointID, SourceSessionID: row.SourceSessionID,
		Workspace: row.Workspace, SourceRevision: uint64(row.SourceRevision), BoundaryID: row.BoundaryID,
		FormatVersion: uint64(row.FormatVersion), Payload: row.Payload, Checksum: row.Checksum,
		CreatedAt: time.UnixMilli(row.CreatedAtMs), Pinned: row.Pinned == 1,
	}
	history, err := s.q.GetCheckpointHistory(ctx, gen.GetCheckpointHistoryParams{SessionID: sessionID, CheckpointID: checkpointID})
	if err == nil {
		checkpoint.History = CheckpointHistory{TranscriptHead: history.TranscriptHead, ReplayHead: history.ReplayHead, StateID: history.StateID}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SessionCheckpoint{}, fmt.Errorf("read checkpoint history: %w", err)
	}
	if !checkpointMetadataValid(checkpoint) || activity.Workspace != checkpoint.Workspace ||
		(row.Pinned != 0 && row.Pinned != 1) || checkpoint.Checksum != checkpointChecksum(checkpoint) {
		return SessionCheckpoint{}, ErrCheckpointCorrupt
	}
	return checkpoint, nil
}

// ListSessionCheckpoints returns oldest-first metadata without payloads, omitting
// expired unpinned rows. An empty list means no saved capability, not a legacy
// history from which the caller may reconstruct checkpoints.
func (s *Store) ListSessionCheckpoints(ctx context.Context, sessionID string) ([]CheckpointSummary, error) {
	var result []CheckpointSummary
	err := s.withReadTx(ctx, func(tx *Store) error {
		activity, err := tx.q.SessionCheckpointActivity(ctx, sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read checkpoint activity: %w", err)
		}
		rows, err := tx.q.ListSessionCheckpoints(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("list checkpoints: %w", err)
		}
		result = make([]CheckpointSummary, 0, len(rows))
		for _, row := range rows {
			if row.Pinned == 0 && activity.UpdatedAt <= time.Now().Add(-CheckpointRetention).Unix() {
				continue
			}
			if row.SourceRevision < 0 || row.FormatVersion <= 0 || row.Workspace != activity.Workspace ||
				!row.PayloadBytes.Valid || row.PayloadBytes.Int64 <= 0 || row.PayloadBytes.Int64 > CheckpointPayloadLimit {
				return ErrCheckpointCorrupt
			}
			result = append(result, CheckpointSummary{
				SessionID: sessionID, ID: row.CheckpointID, SourceSessionID: row.SourceSessionID,
				Workspace: row.Workspace, SourceRevision: uint64(row.SourceRevision), BoundaryID: row.BoundaryID,
				FormatVersion: uint64(row.FormatVersion), Checksum: row.Checksum, CreatedAt: time.UnixMilli(row.CreatedAtMs),
				Pinned: row.Pinned == 1, PayloadBytes: row.PayloadBytes.Int64,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ExpireSessionCheckpoints logically deletes unpinned checkpoint payloads after
// session inactivity. It never deletes conversation history or branch provenance.
// Call on maintenance/startup; no background process is started by this API.
func (s *Store) ExpireSessionCheckpoints(ctx context.Context, now time.Time) (int64, error) {
	count, err := s.q.ExpireSessionCheckpoints(ctx, now.Add(-CheckpointRetention).Unix())
	if err != nil {
		return 0, fmt.Errorf("expire checkpoints: %w", err)
	}
	return count, nil
}

// PinSessionCheckpoint excludes a retained checkpoint from quota/expiry deletion.
// An already expired checkpoint cannot be resurrected by pinning it.
func (s *Store) PinSessionCheckpoint(ctx context.Context, sessionID, checkpointID string, pinned bool) error {
	return s.WithTx(ctx, func(tx *Store) error {
		if _, err := tx.getSessionCheckpoint(ctx, sessionID, checkpointID, time.Now()); err != nil {
			return err
		}
		if _, err := tx.q.PinSessionCheckpoint(ctx, gen.PinSessionCheckpointParams{
			SessionID: sessionID, CheckpointID: checkpointID, Pinned: boolToInt64(pinned),
		}); err != nil {
			return fmt.Errorf("pin checkpoint: %w", err)
		}
		return nil
	})
}

func checkpointMetadataValid(checkpoint SessionCheckpoint) bool {
	return checkpoint.ID != "" && checkpoint.SessionID != "" && checkpoint.SourceSessionID != "" &&
		checkpoint.Workspace != "" && checkpoint.BoundaryID != "" && checkpoint.SourceRevision <= math.MaxInt64 &&
		checkpoint.FormatVersion > 0 && checkpoint.FormatVersion <= math.MaxInt64 &&
		len(checkpoint.Payload) > 0 && len(checkpoint.Payload) <= CheckpointPayloadLimit
}

func checkpointChecksum(checkpoint SessionCheckpoint) string {
	digest := sha256.New()
	writeChecksumString(digest, "session-checkpoint-v1")
	writeChecksumString(digest, checkpoint.SessionID)
	writeChecksumString(digest, checkpoint.ID)
	writeChecksumString(digest, checkpoint.SourceSessionID)
	writeChecksumString(digest, checkpoint.Workspace)
	writeChecksumUint64(digest, checkpoint.SourceRevision)
	writeChecksumString(digest, checkpoint.BoundaryID)
	writeChecksumUint64(digest, checkpoint.FormatVersion)
	writeChecksumUint64(digest, uint64(checkpoint.CreatedAt.UnixMilli()))
	writeChecksumBytes(digest, checkpoint.Payload)
	if checkpoint.History != (CheckpointHistory{}) {
		writeChecksumString(digest, checkpoint.History.TranscriptHead)
		writeChecksumString(digest, checkpoint.History.ReplayHead)
		writeChecksumString(digest, checkpoint.History.StateID)
	}
	return hex.EncodeToString(digest.Sum(nil))
}
