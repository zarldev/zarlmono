// Package task loads SWE-bench-format task definitions. The reference
// dataset is SWE-bench Multilingual (300 tasks across 8 non-Python
// languages) hosted at huggingface.co/datasets/SWE-bench/SWE-bench_Multilingual,
// distributed as a JSONL file with one task object per line.
//
// We deliberately read the raw JSONL rather than the HuggingFace
// "datasets" Python API. Two reasons: (1) eval framework is Go and we
// don't want a Python bridge in the run path; (2) the dataset is
// small enough (~300 rows, a few MB) that streaming the local file is
// trivial and reproducible across environments.
//
// To bootstrap a task set locally:
//
//	huggingface-cli download SWE-bench/SWE-bench_Multilingual \
//	  --repo-type dataset --local-dir /tmp/swebench-multilingual
//
// Then point Load at one of the .jsonl shards inside that dir.
package task

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Spec is the raw SWE-bench task shape, preserved verbatim from the
// dataset's JSONL. Field names match SWE-bench's column names so a
// shard from a future dataset version drops in without remapping.
//
// Not every field is meaningful for every driver — the runner only
// uses a subset to build harness.Task. Hold the rest unparsed for
// future use (the official evaluator wants several of them when
// scoring a submission).
type Spec struct {
	InstanceID           string `json:"instance_id"`
	Repo                 string `json:"repo"`
	BaseCommit           string `json:"base_commit"`
	PatchGold            string `json:"patch"`
	TestPatch            string `json:"test_patch"`
	ProblemStatement     string `json:"problem_statement"`
	HintsText            string `json:"hints_text"`
	CreatedAt            string `json:"created_at"`
	Version              string `json:"version"`
	FailToPass           string `json:"FAIL_TO_PASS"`
	PassToPass           string `json:"PASS_TO_PASS"`
	EnvironmentSetupHash string `json:"environment_setup_hash"`
	Language             string `json:"language"`
}

// Load reads every JSONL row from path into a Spec slice. Returns an
// error on I/O failure or malformed JSON; partial reads are NOT
// silently truncated.
//
// path may be a regular file or "-" for stdin (useful when piping a
// filtered subset from `jq`).
func Load(path string) ([]Spec, error) {
	var rc io.ReadCloser
	if path == "-" {
		rc = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %q: %w", path, err)
		}
		rc = f
	}
	defer rc.Close()

	var specs []Spec
	// SWE-bench rows can be a few KB each (problem_statement +
	// patches); bump the scanner buffer past Go's 64KB default.
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var s Spec
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			return nil, fmt.Errorf("decode line %d: %w", len(specs)+1, err)
		}
		if s.Language == "" {
			s.Language = LanguageFor(s.Repo)
		}
		specs = append(specs, s)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %q: %w", path, err)
	}
	return specs, nil
}

// FilterByLanguage returns the subset of specs matching one of langs
// (case-insensitive). Empty langs = pass-through. Useful for "run
// only the Go tasks first" workflows that hold the comparison
// surface stable while iterating on a single language's verifier.
func FilterByLanguage(specs []Spec, langs ...string) []Spec {
	if len(langs) == 0 {
		return specs
	}
	want := make(map[string]bool, len(langs))
	for _, l := range langs {
		want[strings.ToLower(l)] = true
	}
	out := make([]Spec, 0, len(specs))
	for _, s := range specs {
		if want[strings.ToLower(s.Language)] {
			out = append(out, s)
		}
	}
	return out
}

// Sample returns up to n specs spread evenly across languages by
// taking a stride through the input. Used by the runner to build
// stratified subsets for fast smoke-iteration (50 tasks across 8
// languages instead of all 300) without losing language coverage.
func Sample(specs []Spec, n int) []Spec {
	if n <= 0 || n >= len(specs) {
		return specs
	}
	out := make([]Spec, 0, n)
	stride := float64(len(specs)) / float64(n)
	for i := range n {
		idx := int(float64(i) * stride)
		if idx >= len(specs) {
			break
		}
		out = append(out, specs[idx])
	}
	return out
}
