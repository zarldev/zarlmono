package report_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/db"
	"github.com/zarldev/zarlmono/swebench-eval/report"
)

func TestJSONPreservesUnknownAndUnresolvedOutcomes(t *testing.T) {
	snapshot := db.RunSnapshot{Run: db.RunRecord{ID: "old", ScoreStatus: "not_recorded"}, Results: []db.ResultRecord{
		{InstanceID: "unscored", Error: "infrastructure failure"},
		{InstanceID: "unresolved", Resolved: new(false), TokensIn: 12, TerminalReason: "max_iterations", AttemptVerdicts: `[{"attempt":1,"resolved":false}]`},
		{InstanceID: "resolved", Resolved: new(true), Verified: true, GuardrailRejections: `{"shell_policy":2}`},
	}}
	var output bytes.Buffer
	if err := report.JSON(&output, snapshot); err != nil {
		t.Fatal(err)
	}
	var got struct {
		FormatVersion int              `json:"format_version"`
		Manifest      json.RawMessage  `json:"manifest"`
		Results       []map[string]any `json:"results"`
		ScoreAttempts []any            `json:"score_attempts"`
	}
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.FormatVersion != 1 || string(got.Manifest) != "null" || len(got.Results) != 3 || got.ScoreAttempts == nil {
		t.Fatalf("export shape: %s", output.Bytes())
	}
	if got.Results[0]["resolved"] != nil || got.Results[0]["usage"] != nil || got.Results[1]["resolved"] != false || got.Results[2]["resolved"] != true {
		t.Fatal("export collapsed unknown, unresolved, and resolved")
	}
	usage, ok := got.Results[1]["usage"].(map[string]any)
	if !ok || usage["tokens_in"] != float64(12) || usage["tokens_out"] != float64(0) {
		t.Fatalf("recorded token totals: %v", usage)
	}
	var again bytes.Buffer
	if err := report.JSON(&again, snapshot); err != nil || !bytes.Equal(output.Bytes(), again.Bytes()) {
		t.Fatalf("export is not stable: %v", err)
	}
}

func TestJSONRejectsCorruptMetadataBeforeWriting(t *testing.T) {
	for _, snapshot := range []db.RunSnapshot{
		{Run: db.RunRecord{ManifestJSON: "PRIVATE_CORRUPT_CANARY"}},
		{Results: []db.ResultRecord{{GuardrailRejections: "{"}}},
		{Results: []db.ResultRecord{{AttemptVerdicts: "["}}},
		{ScoreAttempts: []db.ScoreAttemptEvent{{Payload: "{"}}},
	} {
		var out bytes.Buffer
		err := report.JSON(&out, snapshot)
		if !errors.Is(err, report.ErrInvalidMetadata) || out.Len() != 0 {
			t.Fatalf("invalid metadata wrote output: %v, %s", err, out.Bytes())
		}
	}
}

type failedWriter struct{ cause error }

func (w failedWriter) Write([]byte) (int, error) { return 0, w.cause }

func TestJSONReportsOutputFailure(t *testing.T) {
	cause := errors.New("export destination closed")
	if err := report.JSON(failedWriter{cause: cause}, db.RunSnapshot{}); !errors.Is(err, cause) {
		t.Fatalf("write failure: %v", err)
	}
}
