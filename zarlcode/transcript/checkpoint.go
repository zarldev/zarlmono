package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

var (
	// ErrCheckpointUnavailable means no captured canonical checkpoint is present.
	ErrCheckpointUnavailable = errors.New("transcript checkpoint unavailable")
	// ErrInvalidCheckpoint means saved canonical records cannot be restored exactly.
	// Diagnostics deliberately omit record IDs, payloads, and decoder errors, which
	// may contain private conversation data.
	ErrInvalidCheckpoint = errors.New("invalid transcript checkpoint")
	// ErrCheckpointUnsettled means canonical lifecycle entries or queued input have
	// not settled. Passing this check does not establish engine quiescence.
	ErrCheckpointUnsettled = errors.New("transcript checkpoint is unsettled")
)

// Checkpoint owns the exact canonical records captured before a protected prompt.
// It is only the transcript component of a rewind checkpoint: provider context,
// session/workspace identity, integrity, durability and runtime admission belong
// to the application. It must be captured at the boundary, never reconstructed
// by slicing a later thread whose historical entries may have changed.
// The zero value is unavailable, not an initial empty-conversation checkpoint.
// Copies share immutable storage; Records and Restore return independent data.
type Checkpoint struct {
	revision uint64
	records  []Record
	captured bool
}

// CaptureCheckpoint snapshots the entire current thread without modifying it.
// The caller must hold the engine admission reservation and settle event delivery
// before capture, then persist the checkpoint before dispatching the next prompt.
// Terminal failed/interrupted entries are retained; active lifecycle entries and
// undelivered queued prompts return ErrCheckpointUnsettled.
func (t Thread) CaptureCheckpoint() (Checkpoint, error) {
	if err := validateCheckpointEntries(t); err != nil {
		return Checkpoint{}, err
	}
	if err := t.Validate(); err != nil {
		return Checkpoint{}, fmt.Errorf("%w: canonical thread rejected", ErrInvalidCheckpoint)
	}
	records, err := t.RecordsSince(0)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("%w: encode canonical records", ErrInvalidCheckpoint)
	}
	return Checkpoint{revision: t.revision, records: records, captured: true}, nil
}

// CheckpointFromRecords validates saved canonical records without legacy crash
// repair and takes an independent copy, including every payload byte. Records may
// arrive out of order; the returned checkpoint orders them by canonical sequence.
// It does not infer checkpoints from legacy transcripts: callers must first verify
// the enclosing checkpoint's format, integrity and session/workspace provenance.
func CheckpointFromRecords(revision uint64, records []Record) (Checkpoint, error) {
	thread, err := fromRecords(revision, records, false)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("%w: canonical records rejected", ErrInvalidCheckpoint)
	}
	if err := validateCheckpointEntries(thread); err != nil {
		return Checkpoint{}, err
	}
	owned := cloneRecords(records)
	slices.SortFunc(owned, func(a, b Record) int {
		switch {
		case a.Sequence < b.Sequence:
			return -1
		case a.Sequence > b.Sequence:
			return 1
		default:
			return 0
		}
	})
	return Checkpoint{revision: revision, records: owned, captured: true}, nil
}

// Revision returns the exact canonical revision at capture, including zero for
// the initial checkpoint. Revision alone does not establish availability.
func (c Checkpoint) Revision() uint64 { return c.revision }

// Records returns deeply owned persistence rows without re-encoding payloads.
// The zero checkpoint returns ErrCheckpointUnavailable rather than an empty prefix.
func (c Checkpoint) Records() ([]Record, error) {
	if !c.captured {
		return nil, ErrCheckpointUnavailable
	}
	return cloneRecords(c.records), nil
}

// Restore constructs an independent canonical thread from checkpoint-owned
// records, never from the current source thread. No repair or revision changes
// are permitted. It neither activates a branch nor restores provider/file state.
func (c Checkpoint) Restore() (Thread, error) {
	if !c.captured {
		return Thread{}, ErrCheckpointUnavailable
	}
	thread, err := fromRecords(c.revision, c.records, false)
	if err != nil {
		return Thread{}, fmt.Errorf("%w: restore canonical records", ErrInvalidCheckpoint)
	}
	return thread, nil
}

func cloneRecords(records []Record) []Record {
	owned := make([]Record, len(records))
	for i, record := range records {
		owned[i] = record
		owned[i].Payload = bytes.Clone(record.Payload)
	}
	return owned
}

func validateCheckpointEntries(thread Thread) error {
	for _, entry := range thread.entries {
		if !checkpointPayloadValid(entry) {
			return fmt.Errorf("%w: incompatible kind payload", ErrInvalidCheckpoint)
		}
		switch entry.Kind {
		case EntryKinds.ENTRYASSISTANTMESSAGE, EntryKinds.ENTRYREASONING:
			if !entry.Payload.Complete && !entry.Payload.Interrupted {
				return fmt.Errorf("%w: assistant or reasoning is active", ErrCheckpointUnsettled)
			}
		case EntryKinds.ENTRYTOOLCALL:
			if entry.Payload.ToolState == ToolRunning {
				return fmt.Errorf("%w: tool is active", ErrCheckpointUnsettled)
			}
		case EntryKinds.ENTRYSUBAGENT:
			if entry.Payload.Subagent == SubagentPending || entry.Payload.Subagent == SubagentRunning {
				return fmt.Errorf("%w: subagent is active", ErrCheckpointUnsettled)
			}
		case EntryKinds.ENTRYQUEUEDUSER:
			if !entry.Payload.Injected {
				return fmt.Errorf("%w: queued prompt is undelivered", ErrCheckpointUnsettled)
			}
		}
	}
	return nil
}

// Project only kind-supported fields, rejecting all other nonzero fields without
// changing legacy transcript validation. DeepEqual handles the payload's nested
// slices and makes newly added fields unsupported until explicitly accepted here.
func checkpointPayloadValid(entry Entry) bool {
	p := entry.Payload
	var allowed Payload
	switch entry.Kind {
	case EntryKinds.ENTRYUSERMESSAGE:
		allowed = Payload{Text: p.Text, Attachments: p.Attachments}
	case EntryKinds.ENTRYQUEUEDUSER:
		allowed = Payload{Text: p.Text, Injected: p.Injected}
	case EntryKinds.ENTRYASSISTANTMESSAGE, EntryKinds.ENTRYREASONING:
		if p.Complete && p.Interrupted {
			return false
		}
		allowed = Payload{Text: p.Text, Complete: p.Complete, Interrupted: p.Interrupted}
	case EntryKinds.ENTRYTOOLCALL:
		allowed = Payload{
			ToolID: p.ToolID, ParentToolID: p.ParentToolID, ToolName: p.ToolName,
			ExecutionID: p.ExecutionID, ParentExecutionID: p.ParentExecutionID,
			Argument: p.Argument, Effect: p.Effect, ToolState: p.ToolState,
			FailureKind: p.FailureKind, DurationMS: p.DurationMS, Sequence: p.Sequence,
		}
	case EntryKinds.ENTRYDIFF:
		allowed = Payload{Path: p.Path, Diff: p.Diff}
	case EntryKinds.ENTRYPLAN:
		allowed = Payload{Plan: p.Plan}
	case EntryKinds.ENTRYSKILLS:
		allowed = Payload{Skills: p.Skills}
	case EntryKinds.ENTRYSUBAGENT:
		allowed = Payload{
			Text: p.Text, AgentName: p.AgentName, Provider: p.Provider, Model: p.Model,
			Prompt: p.Prompt, SpawnToolID: p.SpawnToolID, Subagent: p.Subagent,
			SpawnExecutionID: p.SpawnExecutionID,
		}
	case EntryKinds.ENTRYINPUTADMISSION:
		allowed = Payload{Text: p.Text, InputAdmission: p.InputAdmission}
	case EntryKinds.ENTRYINPUTWAIT:
		allowed = Payload{Text: p.Text, InputWaiting: p.InputWaiting}
	case EntryKinds.ENTRYNOTICE:
		allowed = Payload{Text: p.Text}
	default:
		return false
	}
	return reflect.DeepEqual(p, allowed)
}
