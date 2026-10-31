package harness

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// ErrExperiment identifies an invalid experiment specification.
var ErrExperiment = errors.New("invalid harness experiment")

const maxExperimentBytes = 256 << 10
const baselineTrialLabel = "baseline"

var candidateLabel = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

type candidate struct {
	Label    string `json:"label"`
	Guidance string `json:"guidance"`
}

type experimentSpec struct {
	Version    int         `json:"version"`
	Repeats    int         `json:"repeats"`
	Candidates []candidate `json:"candidates"`
}

// Experiment owns a validated, immutable snapshot of prompt-guidance candidates.
// Its zero value disables experiments. Baseline trials are added automatically.
type Experiment struct {
	spec experimentSpec
}

// ParseExperiment reads at most 256 KiB of version-1 JSON. It accepts 1–16
// candidates, 1–10 repeats, and at most 16 KiB of nonempty guidance per candidate.
// Labels are unique lowercase path-free identifiers; baseline is reserved.
func ParseExperiment(r io.Reader) (Experiment, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxExperimentBytes+1))
	if err != nil {
		return Experiment{}, fmt.Errorf("read harness experiment: %w", err)
	}
	if len(data) > maxExperimentBytes {
		return Experiment{}, fmt.Errorf("%w: exceeds 256 KiB", ErrExperiment)
	}
	if err := validateExperimentJSON(data); err != nil {
		return Experiment{}, fmt.Errorf("%w: %w", ErrExperiment, err)
	}
	var spec experimentSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return Experiment{}, fmt.Errorf("%w: %w", ErrExperiment, err)
	}
	if spec.Version != 1 || spec.Repeats < 1 || spec.Repeats > 10 || len(spec.Candidates) < 1 || len(spec.Candidates) > 16 {
		return Experiment{}, fmt.Errorf("%w: require version 1, 1–10 repeats and 1–16 candidates", ErrExperiment)
	}
	seen := map[string]bool{baselineTrialLabel: true}
	for _, c := range spec.Candidates {
		if !candidateLabel.MatchString(c.Label) || seen[c.Label] {
			return Experiment{}, fmt.Errorf("%w: invalid or duplicate label %q", ErrExperiment, c.Label)
		}
		if strings.TrimSpace(c.Guidance) == "" || len(c.Guidance) > 16<<10 {
			return Experiment{}, fmt.Errorf("%w: guidance for %q must contain 1–16384 bytes of nonblank text", ErrExperiment, c.Label)
		}
		seen[c.Label] = true
	}
	return Experiment{spec: spec}, nil
}

// MarshalJSON snapshots the parsed input, or null for a disabled experiment.
func (e Experiment) MarshalJSON() ([]byte, error) {
	if e.spec.Version == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(e.spec)
}

// BaselineTrial returns a captured, unchanged baseline for a single refinement round.
func BaselineTrial() Trial {
	return Trial{label: baselineTrialLabel, repeat: 1}
}

// Trial identifies one immutable candidate/repeat pair. Experiment.Trials and
// BaselineTrial construct active trials; zero preserves ordinary driver behavior.
type Trial struct {
	label    string
	guidance string
	repeat   int
}

// Trials returns an independent, deterministic baseline-first sequence per
// repetition. Mutating the returned slice does not change the experiment.
func (e Experiment) Trials() []Trial {
	var trials []Trial
	for repeat := 1; repeat <= e.spec.Repeats; repeat++ {
		trials = append(trials, Trial{label: baselineTrialLabel, repeat: repeat})
		for _, c := range e.spec.Candidates {
			trials = append(trials, Trial{label: c.Label, guidance: c.Guidance, repeat: repeat})
		}
	}
	return trials
}

// DriverName gives this trial a stable result/scoring key, or empty when disabled.
func (t Trial) DriverName() string {
	if t.repeat == 0 {
		return ""
	}
	return fmt.Sprintf("zarlcode-harness-%s-r%d", t.label, t.repeat)
}

// Prompt appends attributed guidance without interpreting it as a template.
// Baseline and disabled trials preserve the canonical prompt byte-for-byte.
func (t Trial) Prompt(canonical string) string {
	if t.guidance == "" {
		return canonical
	}
	return canonical + "\n\n## Experimental guidance: " + t.label + "\n\n" + t.guidance
}

// MarshalJSON records exact candidate bytes and their digest, independently of
// the workspace-specific canonical prompt (captured in prepared requests).
func (t Trial) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Driver         string `json:"driver"`
		Label          string `json:"label"`
		Repeat         int    `json:"repeat"`
		Guidance       string `json:"guidance"`
		GuidanceSHA256 string `json:"guidance_sha256"`
	}{t.DriverName(), t.label, t.repeat, t.guidance, fmt.Sprintf("%x", sha256.Sum256([]byte(t.guidance)))})
}
