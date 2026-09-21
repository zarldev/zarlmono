package rewind

import "github.com/zarldev/zarlmono/zarlcode/transcript"

// savedRecord isolates the versioned payload from the canonical store's Go
// field names. Payload is bytes (base64 in JSON), never re-encoded raw JSON.
type savedRecord struct {
	Sequence uint64 `json:"sequence"`
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	TurnID   string `json:"turn_id"`
	Kind     string `json:"kind"`
	Revision uint64 `json:"revision"`
	Payload  []byte `json:"payload"`
}

func saveRecords(records []transcript.Record) []savedRecord {
	saved := make([]savedRecord, len(records))
	for i, r := range records {
		saved[i] = savedRecord(r)
	}
	return saved
}

func restoreRecords(saved []savedRecord) []transcript.Record {
	records := make([]transcript.Record, len(saved))
	for i, r := range saved {
		records[i] = transcript.Record(r)
	}
	return records
}
