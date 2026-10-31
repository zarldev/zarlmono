package harness_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

func TestExperimentRejectsAmbiguousJSONAndMalformedUnicode(t *testing.T) {
	for name, input := range map[string]string{
		"duplicate root":        `{"version":2,"version":1,"repeats":1,"candidates":[{"label":"a","guidance":"x"}]}`,
		"duplicate guidance":    `{"version":1,"repeats":1,"candidates":[{"label":"a","guidance":"x","guidance":"y"}]}`,
		"case folded root":      strings.Replace(experimentInput, "version", "Version", 1),
		"case folded candidate": strings.Replace(experimentInput, "guidance", "Guidance", 1),
		"invalid utf8":          strings.Replace(experimentInput, "Read", "\xff", 1),
		"high surrogate":        strings.Replace(experimentInput, "Read", `\ud800`, 1),
		"low surrogate":         strings.Replace(experimentInput, "Read", `\udfff`, 1),
		"wrong pair":            strings.Replace(experimentInput, "Read", `\ud800\u0020`, 1),
		"escaped duplicate":     `{"version":1,"repeats":1,"candidates":[{"label":"a","guidance":"x","\u0067uidance":"y"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := harness.ParseExperiment(strings.NewReader(input)); !errors.Is(err, harness.ErrExperiment) {
				t.Fatalf("error = %v, want ErrExperiment", err)
			}
		})
	}
	for _, text := range []string{`\ud83d\ude80`, `\\ud800`, `\ufffd`, `\u0020`, `\/`} {
		if _, err := harness.ParseExperiment(strings.NewReader(strings.Replace(experimentInput, "Read", text, 1))); err != nil {
			t.Fatalf("valid escape %q: %v", text, err)
		}
	}
}

func TestEvidencePreservesNonUTF8TaskAndPatch(t *testing.T) {
	dir := t.TempDir()
	const original = "source bytes \xff\xfe"
	e, err := harness.OpenTrialEvidence(dir, "run", harness.Trial{}, harness.Task{Problem: original})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if err := e.Finish(harness.Result{Diff: original, Err: errors.New(original)}); err != nil {
		t.Fatal(err)
	}
	events, _ := readEvidence(t, dir)
	for i, field := range []string{"task_prompt", "diff"} {
		var fields map[string]struct {
			Text  string `json:"text"`
			Bytes []byte `json:"bytes_base64"`
		}
		// Only decode the relevant text field; the other metadata has scalar values.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(events[i].Data, &raw); err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(map[string]json.RawMessage{field: raw[field]})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if fields[field].Text != "" || string(fields[field].Bytes) != original {
			t.Fatalf("%s was repaired rather than preserved", field)
		}
	}
}
