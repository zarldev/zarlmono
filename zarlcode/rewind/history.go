package rewind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
	"github.com/zarldev/zarlmono/zkit/agent/runner"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/db"
)

// HistoryFormatVersion identifies checkpoints containing only immutable history
// references. Boundary metadata is separately content-addressed, not duplicated
// with the full transcript or the terminal model context.
const HistoryFormatVersion uint64 = 2

type historyPayload struct {
	StateID string `json:"state_id"`
}

// CaptureHistory stores an immutable boundary without publishing its checkpoint.
// Production dispatch uses PrepareHistory and Store.CommitBeforeTurn instead so
// the guarded recovery row, retained batches and checkpoint commit together.
func CaptureHistory(ctx context.Context, store *db.Store, input CaptureInput) (Checkpoint, error) {
	prepared, err := PrepareHistory(input)
	if err != nil {
		return Checkpoint{}, err
	}
	ref, err := store.CaptureSessionHistory(ctx, input.SessionID, prepared.Entries, prepared.State)
	if err != nil {
		return Checkpoint{}, err
	}
	record := prepared.Checkpoint
	record.History = ref
	return Load(ctx, store, record)
}

// PrepareHistory serializes validated boundary metadata and display entries
// without writing storage or treating compacted working context as replay.
// CommitBeforeTurn binds the replay head after retained batches inside its commit.
func PrepareHistory(input CaptureInput) (db.HistoryCheckpoint, error) {
	records, err := input.Transcript.Records()
	if err != nil {
		return db.HistoryCheckpoint{}, ErrUnavailable
	}
	state := payload{
		Version: FormatVersion, ID: input.ID, SourceID: input.SessionID, Workspace: input.Workspace,
		Boundary: input.Boundary, Revision: input.Transcript.Revision(), Records: saveRecords(records),
		Context: llm.CloneMessages(input.Context), Target: input.Target, Plan: clonePlan(input.Plan),
	}
	if state.Context == nil {
		state.Context = []llm.Message{}
	}
	state.Boundary.Attachments = llm.CloneContentParts(input.Boundary.Attachments)
	if err := validatePayload(state); err != nil {
		return db.HistoryCheckpoint{}, err
	}
	entries := make([]db.TranscriptEntry, len(records))
	for i, record := range state.Records {
		entries[i] = db.TranscriptEntry{Sequence: record.Sequence, EntryID: record.ID, ParentID: record.ParentID,
			TurnID: record.TurnID, Kind: record.Kind, Revision: record.Revision, PayloadJSON: record.Payload}
	}
	state.Records, state.Context = nil, nil
	data, err := json.Marshal(state)
	if err != nil {
		return db.HistoryCheckpoint{}, ErrInvalid
	}
	digest := sha256.Sum256(data)
	ref := db.CheckpointHistory{StateID: hex.EncodeToString(digest[:])}
	envelope, err := json.Marshal(historyPayload{StateID: ref.StateID})
	if err != nil {
		return db.HistoryCheckpoint{}, ErrInvalid
	}
	record := db.SessionCheckpoint{SessionID: input.SessionID, ID: input.ID,
		SourceSessionID: input.SessionID, Workspace: input.Workspace, SourceRevision: state.Revision,
		BoundaryID: input.Boundary.PromptID, FormatVersion: HistoryFormatVersion, Payload: envelope, History: ref}
	return db.HistoryCheckpoint{Checkpoint: record, Entries: entries, State: data}, nil
}

// Load resolves a checkpoint's immutable prefixes and builds model conversation
// from recorded replay occurrences, never display text or historical tool calls.
// The caller rebuilds current system/tool context through normal runner request
// preparation. Legacy snapshots remain decodable, but missing replay is not
// backfilled from them or advertised as newly captured history.
func Load(ctx context.Context, store *db.Store, record db.SessionCheckpoint) (Checkpoint, error) {
	if record.FormatVersion != HistoryFormatVersion {
		return Decode(record)
	}
	var ref historyPayload
	if !strictJSON(record.Payload, &ref) || ref.StateID == "" || ref.StateID != record.History.StateID {
		return Checkpoint{}, ErrInvalid
	}
	entries, replay, data, err := store.ReadCheckpointHistory(ctx, record.History)
	if err != nil {
		return Checkpoint{}, err
	}
	var state payload
	if !strictJSON(data, &state) || state.Records != nil || state.Context != nil {
		return Checkpoint{}, ErrInvalid
	}
	state.Records = make([]savedRecord, len(entries))
	for i, entry := range entries {
		state.Records[i] = savedRecord{Sequence: entry.Sequence, ID: entry.EntryID, ParentID: entry.ParentID,
			TurnID: entry.TurnID, Kind: entry.Kind, Revision: entry.Revision, Payload: entry.PayloadJSON}
	}
	state.Context = make([]llm.Message, 0, len(replay))
	for _, value := range replay {
		occurrence, err := runner.UnmarshalReplayMessage(value)
		if err != nil {
			return Checkpoint{}, ErrInvalid
		}
		if !occurrence.AdmittedToModelContext() {
			continue
		} // observed, but never admitted as model input
		state.Context = append(state.Context, occurrence.Message)
	}
	if state.ID != record.ID || state.SourceID != record.SourceSessionID || state.Workspace != record.Workspace ||
		state.Revision != record.SourceRevision || state.Boundary.PromptID != record.BoundaryID || validatePayload(state) != nil {
		return Checkpoint{}, ErrInvalid
	}
	canonical, err := transcript.CheckpointFromRecords(state.Revision, restoreRecords(state.Records))
	if err != nil {
		return Checkpoint{}, ErrInvalid
	}
	return Checkpoint{record: record, state: state, canonical: canonical, valid: true}, nil
}
