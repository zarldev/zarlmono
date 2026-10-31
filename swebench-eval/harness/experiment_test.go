package harness_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/swebench-eval/harness"
)

const experimentInput = `{"version":1,"repeats":2,"candidates":[{"label":"navigate","guidance":"Read the owning package first. {{literal}}"}]}`

func TestExperimentTrialsSnapshotAndBaseline(t *testing.T) {
	experiment, err := harness.ParseExperiment(strings.NewReader(experimentInput))
	if err != nil {
		t.Fatal(err)
	}
	trials := experiment.Trials()
	var names []string
	for _, trial := range trials {
		driver := &harness.ZarlcodeDriver{Trial: trial}
		names = append(names, driver.Name())
	}
	want := []string{"zarlcode-harness-baseline-r1", "zarlcode-harness-navigate-r1", "zarlcode-harness-baseline-r2", "zarlcode-harness-navigate-r2"}
	if !slices.Equal(names, want) {
		t.Fatalf("trial names = %v", names)
	}
	const canonical = "canonical prompt\n"
	if trials[0].Prompt(canonical) != canonical || (harness.Trial{}).Prompt(canonical) != canonical {
		t.Fatal("baseline changed")
	}
	if got := trials[1].Prompt(canonical); !strings.HasPrefix(got, canonical) || !strings.Contains(got, "{{literal}}") {
		t.Fatalf("guidance was not appended literally: %q", got)
	}
	data, err := json.Marshal(trials[1])
	if err != nil {
		t.Fatal(err)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte("Read the owning package first. {{literal}}")))
	if !strings.Contains(string(data), wantHash) {
		t.Fatalf("missing guidance identity: %s", data)
	}
	trials[1] = harness.Trial{}
	if experiment.Trials()[1].DriverName() != want[1] {
		t.Fatal("returned slice aliases experiment state")
	}
	data, err = json.Marshal(experiment)
	if err != nil || string(data) != experimentInput {
		t.Fatalf("snapshot = %s: %v", data, err)
	}
	if got := (harness.Experiment{}).Trials(); len(got) != 0 {
		t.Fatalf("zero experiment created trials: %v", got)
	}
}

func TestExperimentRejectsInvalidInput(t *testing.T) {
	cases := map[string]string{
		"empty": "", "null": "null", "trailing": experimentInput + " {}",
		"version":        strings.Replace(experimentInput, `"version":1`, `"version":2`, 1),
		"unknown":        strings.Replace(experimentInput, `"version":1`, `"version":1,"scripts":[]`, 1),
		"zero repeats":   strings.Replace(experimentInput, `"repeats":2`, `"repeats":0`, 1),
		"many repeats":   strings.Replace(experimentInput, `"repeats":2`, `"repeats":11`, 1),
		"no candidates":  `{"version":1,"repeats":1,"candidates":[]}`,
		"reserved":       strings.ReplaceAll(experimentInput, "navigate", "baseline"),
		"path":           strings.ReplaceAll(experimentInput, "navigate", "../outside"),
		"uppercase":      strings.ReplaceAll(experimentInput, "navigate", "Navigate"),
		"empty guidance": `{"version":1,"repeats":1,"candidates":[{"label":"a","guidance":" "}]}`,
		"duplicate":      `{"version":1,"repeats":1,"candidates":[{"label":"a","guidance":"one"},{"label":"a","guidance":"two"}]}`,
		"large guidance": `{"version":1,"repeats":1,"candidates":[{"label":"a","guidance":"` + strings.Repeat("x", 16385) + `"}]}`,
		"large input":    strings.Repeat(" ", (256<<10)+1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := harness.ParseExperiment(strings.NewReader(input))
			if !errors.Is(err, harness.ErrExperiment) {
				t.Fatalf("error = %v, want ErrExperiment", err)
			}
		})
	}
}
