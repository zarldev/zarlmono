package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// CheckpointBranch is a prepared application transition. The application must
// strictly decode the checkpoint payload, build the child context and Entries
// from that immutable payload (never current source entries), and validate its
// provider target before calling. CheckpointChecksum binds that validation to the
// exact stored bytes rechecked inside the transaction. Runtime admission remains
// the caller's responsibility; this operation provides database atomicity only.
// Child must have a fresh ID. Its workspace must equal Workspace. Tool history
// is shared through immutable replay; mutable latest-per-call projections are
// never copied into a branch.
type CheckpointBranch struct {
	SourceSessionID        string
	Workspace              string
	ExpectedSourceRevision uint64
	CheckpointID           string
	CheckpointChecksum     string
	Child                  SessionRecord
	Entries                []TranscriptEntry
	// ToolCallIDs is ignored. Call IDs cannot identify immutable occurrences.
	// Read the child's canonical replay instead.
	ToolCallIDs []string
	// ReplaySuffix contains new continuation occurrences, never copied history.
	// It is appended to the shared checkpoint prefix in the branch transaction.
	ReplaySuffix [][]byte
}

// SessionBranch records durable provenance independently of source lifetime.
// SourceRevision is the source head at branching, not the checkpoint boundary.
type SessionBranch struct {
	SessionID          string
	SourceSessionID    string
	SourceCheckpointID string
	SourceRevision     uint64
	CheckpointChecksum string
	CreatedAt          time.Time
}

// SaveCheckpointSource preserves the current recovery head and draft before a
// branch transition. It refuses changed source content or head without depending
// on workspace selection, and does not advance or rewrite canonical history.
// Callers must exclude local writers and carry the content version observed
// before preparing the operation.
func (s *Store) SaveCheckpointSource(ctx context.Context, record SessionRecord, revision uint64, expected SessionContentVersion) error {
	return s.WithTx(ctx, func(tx *Store) error {
		return tx.saveCheckpointSource(ctx, record, revision, expected, nil)
	})
}

func (s *Store) saveCheckpointSource(ctx context.Context, record SessionRecord, revision uint64, expected SessionContentVersion, history []SessionHistoryBatch) error {
	tx := s
	if err := tx.checkSessionVersion(ctx, record.ID, expected); err != nil {
		return err
	}
	if err := tx.checkCheckpointSource(ctx, record.ID, record.Workspace, revision); err != nil {
		return err
	}
	if err := tx.SaveSession(ctx, record); err != nil {
		return err
	}
	return tx.saveSessionHistory(ctx, record.ID, history)
}

// CreateCheckpointBranch commits a new session, exact canonical prefix, an
// independent checkpoint copy, immutable replay ancestry, provenance, and the
// active pointer together. It preserves every source row and rejects stale source
// revisions, active pointers, checkpoint bytes, or existing child IDs. Empty
// prefixes and empty model contexts are supported without legacy repair.
// Publish in-memory activation only after this method returns nil.
func (s *Store) CreateCheckpointBranch(ctx context.Context, branch CheckpointBranch) error {
	_, err := s.createCheckpointBranch(ctx, branch, func(tx *Store) error {
		return tx.checkCheckpointSourceSelection(ctx, branch.SourceSessionID, branch.Workspace, branch.ExpectedSourceRevision, branch.SourceSessionID)
	})
	return err
}

// CreateCheckpointBranchVersioned rejects changes to expected source content and
// commits a branch, returning its child's complete row version from the same
// transaction. expected must come from the source snapshot or preservation receipt.
func (s *Store) CreateCheckpointBranchVersioned(ctx context.Context, branch CheckpointBranch, expected SessionContentVersion) (SessionContentVersion, error) {
	return s.createCheckpointBranch(ctx, branch, func(tx *Store) error {
		if err := tx.checkSessionVersion(ctx, branch.SourceSessionID, expected); err != nil {
			return err
		}
		return tx.checkCheckpointSourceSelection(ctx, branch.SourceSessionID, branch.Workspace, branch.ExpectedSourceRevision, branch.SourceSessionID)
	})
}

// CreateCheckpointRecoveryBranch creates a separately identifiable continuation
// from an explicitly selected checkpoint without changing any source rows. The
// source need not be active. expected and activeSession must be observations from
// the confirmed preview; an empty activeSession means no selection was stored.
// Changes to source content, transcript, or active selection reject atomically.
// It does not build a provider, activate a runtime, or execute the saved draft.
func (s *Store) CreateCheckpointRecoveryBranch(ctx context.Context, branch CheckpointBranch, expected SessionContentVersion, activeSession string) error {
	_, err := s.createCheckpointBranch(ctx, branch, func(tx *Store) error {
		if err := tx.checkSessionVersion(ctx, branch.SourceSessionID, expected); err != nil {
			return err
		}
		return tx.checkCheckpointSourceSelection(ctx, branch.SourceSessionID, branch.Workspace, branch.ExpectedSourceRevision, activeSession)
	})
	return err
}

func (s *Store) createCheckpointBranch(ctx context.Context, branch CheckpointBranch, checkSource func(*Store) error) (SessionContentVersion, error) {
	if branch.Child.ID == "" || branch.Child.ID == branch.SourceSessionID || branch.Child.Workspace != branch.Workspace {
		return SessionContentVersion{}, ErrCheckpointConflict
	}
	if !json.Valid(branch.Child.ContextJSON) {
		return SessionContentVersion{}, ErrCheckpointCorrupt
	}
	sourceRevision, err := sqliteInteger("branch source revision", branch.ExpectedSourceRevision)
	if err != nil {
		return SessionContentVersion{}, ErrCheckpointConflict
	}
	var version SessionContentVersion
	err = s.WithTx(ctx, func(tx *Store) error {
		if err := checkSource(tx); err != nil {
			return err
		}
		now := time.Now().Truncate(time.Millisecond)
		checkpoint, err := tx.getSessionCheckpoint(ctx, branch.SourceSessionID, branch.CheckpointID, now)
		if err != nil {
			return err
		}
		if checkpoint.Checksum != branch.CheckpointChecksum || checkpoint.Workspace != branch.Workspace || checkpoint.SourceRevision > branch.ExpectedSourceRevision {
			return ErrCheckpointConflict
		}
		if err := validateCheckpointBranchEntries(checkpoint.SourceRevision, branch.Entries); err != nil {
			return err
		}
		if _, err := tx.q.SessionCheckpointActivity(ctx, branch.Child.ID); err == nil {
			return ErrCheckpointConflict
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check branch destination: %w", err)
		}
		if err := tx.saveCheckpointChild(ctx, branch.Child, now); err != nil {
			return fmt.Errorf("save checkpoint child: %w", err)
		}
		if err := tx.insertCheckpointBranchTranscript(ctx, branch.Child.ID, checkpoint.SourceRevision, branch.Entries, now); err != nil {
			return err
		}
		provenance := SessionBranch{
			SessionID: branch.Child.ID, SourceSessionID: branch.SourceSessionID, SourceCheckpointID: checkpoint.ID,
			SourceRevision: branch.ExpectedSourceRevision, CheckpointChecksum: checkpoint.Checksum, CreatedAt: now,
		}
		if err := tx.q.InsertSessionBranch(ctx, gen.InsertSessionBranchParams{
			SessionID: branch.Child.ID, SourceSessionID: branch.SourceSessionID, SourceCheckpointID: checkpoint.ID,
			SourceRevision: sourceRevision, CheckpointChecksum: checkpoint.Checksum, CreatedAtMs: now.UnixMilli(),
			Checksum: branchChecksum(provenance),
		}); err != nil {
			return fmt.Errorf("save branch provenance: %w", err)
		}
		checkpoint.SessionID = branch.Child.ID
		checkpoint.Pinned = false
		checkpoint.Checksum = checkpointChecksum(checkpoint)
		if err := tx.insertCheckpoint(ctx, checkpoint); err != nil {
			return err
		}
		if checkpoint.History != (CheckpointHistory{}) {
			if err := tx.appendSessionReplay(ctx, branch.Child.ID, branch.ReplaySuffix); err != nil {
				return err
			}
		}
		if err := tx.q.UpsertSetting(ctx, gen.UpsertSettingParams{
			Workspace: branch.Workspace, Key: activeSessionSettingKey, Value: branch.Child.ID, UpdatedAt: now.Unix(),
		}); err != nil {
			return fmt.Errorf("activate checkpoint child: %w", err)
		}
		version, err = tx.SessionVersion(ctx, branch.Child.ID)
		return err
	})
	if err != nil {
		return SessionContentVersion{}, err
	}
	return version, nil
}

func validateCheckpointBranchEntries(revision uint64, entries []TranscriptEntry) error {
	if revision == 0 && len(entries) == 0 {
		return nil
	}
	if err := validateStoredTranscript(TranscriptUpdate{Revision: revision}, entries); err != nil {
		// The legacy validator includes private entry identities in diagnostics.
		return ErrCheckpointCorrupt
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if seen[entry.EntryID] || (entry.ParentID != "" && !seen[entry.ParentID]) {
			return ErrCheckpointCorrupt
		}
		seen[entry.EntryID] = true
	}
	return nil
}

func (s *Store) insertCheckpointBranchTranscript(ctx context.Context, sessionID string, revision uint64, entries []TranscriptEntry, now time.Time) error {
	storedRevision, err := sqliteInteger("branch revision", revision)
	if err != nil {
		return ErrCheckpointCorrupt
	}
	if err := s.q.EnsureSessionTranscript(ctx, gen.EnsureSessionTranscriptParams{
		SessionID: sessionID, Checksum: transcriptChecksum(sessionID, 0, nil), FormatVersion: int64(SessionTranscriptFormatVersion),
		CreatedAtMs: now.UnixMilli(), UpdatedAtMs: now.UnixMilli(),
	}); err != nil {
		return fmt.Errorf("create branch transcript: %w", err)
	}
	for _, entry := range entries {
		sequence, err := sqliteInteger("branch entry sequence", entry.Sequence)
		if err != nil {
			return ErrCheckpointCorrupt
		}
		entryRevision, err := sqliteInteger("branch entry revision", entry.Revision)
		if err != nil {
			return ErrCheckpointCorrupt
		}
		if err := s.q.UpsertSessionTranscriptEntry(ctx, gen.UpsertSessionTranscriptEntryParams{
			SessionID: sessionID, Sequence: sequence, EntryID: entry.EntryID, ParentID: entry.ParentID,
			TurnID: entry.TurnID, Kind: entry.Kind, PayloadJson: string(entry.PayloadJSON), Revision: entryRevision,
		}); err != nil {
			return fmt.Errorf("save branch transcript entry: %w", err)
		}
	}
	if _, err := s.q.AdvanceSessionTranscript(ctx, gen.AdvanceSessionTranscriptParams{
		SessionID: sessionID, ExpectedRevision: 0, Revision: storedRevision, Checksum: transcriptChecksum(sessionID, revision, entries),
		FormatVersion: int64(SessionTranscriptFormatVersion), UpdatedAtMs: now.UnixMilli(),
	}); err != nil {
		return fmt.Errorf("save branch transcript revision: %w", err)
	}
	return nil
}

// GetSessionBranch returns provenance even when the source was deleted or its
// checkpoints expired. ErrNotFound distinguishes sessions without provenance.
func (s *Store) GetSessionBranch(ctx context.Context, sessionID string) (SessionBranch, error) {
	row, err := s.read.GetSessionBranch(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionBranch{}, ErrNotFound
	}
	if err != nil {
		return SessionBranch{}, fmt.Errorf("read branch provenance: %w", err)
	}
	if row.SourceRevision < 0 {
		return SessionBranch{}, ErrCheckpointCorrupt
	}
	provenance := SessionBranch{
		SessionID: row.SessionID, SourceSessionID: row.SourceSessionID, SourceCheckpointID: row.SourceCheckpointID,
		SourceRevision: uint64(row.SourceRevision), CheckpointChecksum: row.CheckpointChecksum, CreatedAt: time.UnixMilli(row.CreatedAtMs),
	}
	if row.Checksum != branchChecksum(provenance) {
		return SessionBranch{}, ErrCheckpointCorrupt
	}
	return provenance, nil
}

// Use generated bindings directly here: legacy SaveSession errors include
// private session identifiers, which checkpoint diagnostics must not expose.
func (s *Store) saveCheckpointChild(ctx context.Context, child SessionRecord, now time.Time) error {
	if err := s.q.UpsertSession(ctx, gen.UpsertSessionParams{
		ID: child.ID, Workspace: child.Workspace, Label: child.Label, LabelManual: boolToInt64(child.LabelManual),
		AgentName: child.AgentName, Provider: child.Provider, Model: child.Model,
		ContextJson: string(child.ContextJSON), PendingJson: string(orEmpty(child.PendingJSON, "[]")),
		LastUsageJson: string(orEmpty(child.LastUsageJSON, "null")), DiffBodiesJson: string(orEmpty(child.DiffBodiesJSON, "{}")),
		PlanJson: string(orEmpty(child.PlanJSON, "null")), MessageCount: int64(child.MessageCount),
		CreatedAt: now.Unix(), UpdatedAt: now.Unix(), ChangedFileCount: int64(child.ChangedFileCount),
		PlanCompletedCount: int64(child.PlanCompletedCount), PlanTotalCount: int64(child.PlanTotalCount),
	}); err != nil {
		return fmt.Errorf("insert checkpoint child: %w", err)
	}
	return nil
}

func branchChecksum(branch SessionBranch) string {
	digest := sha256.New()
	writeChecksumString(digest, "session-branch-v1")
	writeChecksumString(digest, branch.SessionID)
	writeChecksumString(digest, branch.SourceSessionID)
	writeChecksumString(digest, branch.SourceCheckpointID)
	writeChecksumUint64(digest, branch.SourceRevision)
	writeChecksumString(digest, branch.CheckpointChecksum)
	writeChecksumUint64(digest, uint64(branch.CreatedAt.UnixMilli()))
	return hex.EncodeToString(digest.Sum(nil))
}
