package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
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
		record, err := t.recordAt(sequence)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (t Thread) recordAt(index int) (Record, error) {
	entry := t.entries[index]
	payload, err := json.Marshal(entry.Payload)
	if err != nil {
		return Record{}, fmt.Errorf("encode transcript entry %q: %w", entry.ID, err)
	}
	return Record{
		Sequence: uint64(index) + 1, ID: entry.ID, ParentID: entry.ParentID, //nolint:gosec // G115: the slice access above already bounds index to a nonnegative int.
		TurnID: entry.TurnID, Kind: entry.Kind.String(), Revision: entry.Revision, Payload: payload,
	}, nil
}

func (b *Builder) changedRecords() ([]Record, error) {
	// Events can touch a child before its parent, or append then update the
	// same entry. Emit each final value once, in canonical sequence order.
	slices.Sort(b.changed)
	indices := slices.Compact(b.changed)
	records := make([]Record, 0, len(indices))
	for _, index := range indices {
		record, err := b.thread.recordAt(index)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// FromRecords validates ordered persistence rows and restores a canonical thread.
func FromRecords(revision uint64, records []Record) (Thread, error) {
	return fromRecords(revision, records, true)
}

func fromRecords(revision uint64, records []Record, recoverOpenAssistant bool) (Thread, error) {
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
		if recoverOpenAssistant && entry.Kind == EntryKinds.ENTRYASSISTANTMESSAGE && entry.Payload.Text == "" &&
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
