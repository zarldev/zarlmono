package transcript_test

import (
	"bytes"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

func TestToolOutputChangesAndSnapshotsRemainIndependent(t *testing.T) {
	t.Parallel()
	reducer := transcript.NewReducer()
	if _, err := reducer.Apply(transcript.ToolStarted{TurnID: "turn", ExecutionID: "call", ToolID: "call", Name: "bash"}); err != nil {
		t.Fatal(err)
	}
	first, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "call", ToolID: "call", Output: "first preview"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != 1 {
		t.Fatalf("records = %d", len(first.Records))
	}
	snapshot := reducer.Thread()
	payload := bytes.Clone(first.Records[0].Payload)
	second, err := reducer.Apply(transcript.ToolOutputUpdated{ExecutionID: "call", ToolID: "call", Output: "second preview"})
	if err != nil {
		t.Fatal(err)
	}
	if second.BeforeRevision != first.AfterRevision || second.AfterRevision != first.AfterRevision+1 || len(second.Records) != 1 {
		t.Fatal("incorrect change revision or record count")
	}
	if snapshot.Entries()[0].Payload.ToolOutputPreview != "first preview" || !bytes.Equal(first.Records[0].Payload, payload) {
		t.Fatal("later update mutated retained snapshot or change")
	}
	clear(second.Records[0].Payload)
	second.Records[0].ID = "changed by consumer"
	if reducer.Thread().Entries()[0].Payload.ToolOutputPreview != "second preview" {
		t.Fatal("returned record aliases reducer state")
	}
	if err := reducer.Thread().Validate(); err != nil {
		t.Fatal(err)
	}
}
