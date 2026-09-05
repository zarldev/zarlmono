package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Record is one renderer-independent persistence row.
type Record struct {
	Sequence uint64
	ID       string
	ParentID string
	TurnID   string
	Kind     string
	Revision uint64
	Payload  []byte
}

// RecordsSince returns entries appended or changed after revision.
func (t Thread) RecordsSince(revision uint64) ([]Record, error) {
	if revision > t.revision {
		return nil, fmt.Errorf("transcript records: revision %d exceeds thread revision %d", revision, t.revision)
	}
	records := make([]Record, 0)
	for sequence, entry := range t.entries {
		if entry.Revision <= revision {
			continue
		}
		payload, err := json.Marshal(entry.Payload)
		if err != nil {
			return nil, fmt.Errorf("encode transcript entry %q: %w", entry.ID, err)
		}
		records = append(records, Record{
			Sequence: uint64(sequence + 1), ID: entry.ID, ParentID: entry.ParentID,
			TurnID: entry.TurnID, Kind: entry.Kind.String(), Revision: entry.Revision, Payload: payload,
		})
	}
	return records, nil
}

// FromRecords validates ordered persistence rows and restores a canonical thread.
func FromRecords(revision uint64, records []Record) (Thread, error) {
	sorted := append([]Record(nil), records...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sequence < sorted[j].Sequence })
	thread := Thread{revision: revision, entries: make([]Entry, 0, len(sorted))}
	for i, record := range sorted {
		if record.Sequence != uint64(i+1) {
			return Thread{}, fmt.Errorf("decode transcript: sequence %d at position %d", record.Sequence, i+1)
		}
		if record.Revision == 0 || record.Revision > revision {
			return Thread{}, fmt.Errorf("decode transcript entry %q: invalid revision %d for thread revision %d", record.ID, record.Revision, revision)
		}
		kind, err := ParseEntryKind(record.Kind)
		if err != nil {
			return Thread{}, fmt.Errorf("decode transcript entry %q kind %q: %w", record.ID, record.Kind, err)
		}
		var payload Payload
		decoder := json.NewDecoder(bytes.NewReader(record.Payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return Thread{}, fmt.Errorf("decode transcript entry %q: %w", record.ID, err)
		}
		// Affected older builds recorded the UNKNOWN enum sentinel on successful
		// tools. It represented no failure, so normalize that exact legacy shape.
		if kind == EntryKinds.ENTRYTOOLCALL && payload.ToolState == ToolSucceeded && payload.FailureKind == "unknown" {
			payload.FailureKind = ""
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return Thread{}, fmt.Errorf("decode transcript entry %q: trailing payload data", record.ID)
		}
		thread.entries = append(thread.entries, Entry{
			ID: record.ID, ParentID: record.ParentID, TurnID: record.TurnID,
			Kind: kind, Revision: record.Revision, Payload: payload,
		})
	}
	// TurnStarted is persisted immediately so crashes retain the user-visible
	// lifecycle. If the process died before the first token, recover that exact
	// runtime-open shape before applying quiescent transcript validation.
	for i := range thread.entries {
		entry := &thread.entries[i]
		if entry.Kind == EntryKinds.ENTRYASSISTANTMESSAGE && entry.Payload.Text == "" &&
			!entry.Payload.Complete && !entry.Payload.Interrupted {
			thread.revision++
			entry.Revision = thread.revision
			entry.Payload.Interrupted = true
		}
	}
	if err := thread.Validate(); err != nil {
		return Thread{}, err
	}
	return thread, nil
}
