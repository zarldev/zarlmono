package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// GuidanceDecision is a validated proposal/composition with evidence provenance.
// NoChange is an explicit decision to stop, not a failed or silently skipped call.
type GuidanceDecision struct {
	Version     int        `json:"version"`
	NoChange    bool       `json:"no_change"`
	Rationale   string     `json:"rationale"`
	EvidenceIDs []string   `json:"evidence_ids"`
	Experiment  Experiment `json:"experiment"`
}

// ParseGuidanceDecision validates a bounded model response against the supplied
// evidence and exact stage labels. No code fences, repairs, or implicit retries
// are accepted. expectedLabels is trusted stage configuration, not model input.
func ParseGuidanceDecision(data []byte, expectedLabels []string, evidence []EvidenceView) (GuidanceDecision, error) {
	invalid := func(reason string) (GuidanceDecision, error) {
		return GuidanceDecision{}, fmt.Errorf("%w: %s", ErrExperiment, reason)
	}
	if len(data) > 16<<10 || !utf8.Valid(data) || !json.Valid(data) {
		return invalid("decision must be bounded UTF-8 JSON")
	}
	if err := validateSurrogates(data); err != nil {
		return invalid("invalid decision Unicode")
	}
	fields, err := experimentObject(data, []string{"version", "no_change", "rationale", "evidence_ids", "experiment"})
	if err != nil {
		return GuidanceDecision{}, fmt.Errorf("%w: decision: %w", ErrExperiment, err)
	}
	boolean := bytes.TrimSpace(fields["no_change"])
	if !bytes.Equal(boolean, []byte("true")) && !bytes.Equal(boolean, []byte("false")) {
		return invalid("no_change must be explicit true or false")
	}
	var wire struct {
		Version     int             `json:"version"`
		NoChange    bool            `json:"no_change"`
		Rationale   string          `json:"rationale"`
		EvidenceIDs []string        `json:"evidence_ids"`
		Experiment  json.RawMessage `json:"experiment"`
	}
	if err = json.Unmarshal(data, &wire); err != nil {
		return GuidanceDecision{}, fmt.Errorf("%w: decision: %w", ErrExperiment, err)
	}
	if wire.Version != 1 || strings.TrimSpace(wire.Rationale) == "" || len(wire.Rationale) > 2048 || len(wire.EvidenceIDs) == 0 || len(wire.EvidenceIDs) > 32 {
		return invalid("invalid decision version, rationale or citation count")
	}
	known := map[string]bool{}
	for _, view := range evidence {
		known[view.ID] = true
		for _, observation := range view.Observations {
			known[observation.ID] = true
		}
	}
	seen := map[string]bool{}
	for _, id := range wire.EvidenceIDs {
		if !known[id] || seen[id] {
			return invalid("unknown or duplicate evidence citation")
		}
		seen[id] = true
	}
	decision := GuidanceDecision{Version: 1, NoChange: wire.NoChange, Rationale: wire.Rationale, EvidenceIDs: wire.EvidenceIDs}
	if wire.NoChange {
		if len(wire.Experiment) != 0 && !bytes.Equal(bytes.TrimSpace(wire.Experiment), []byte("null")) {
			return invalid("no_change must not carry candidates")
		}
		return decision, nil
	}
	decision.Experiment, err = ParseExperiment(bytes.NewReader(wire.Experiment))
	if err != nil {
		return GuidanceDecision{}, fmt.Errorf("parse decision experiment: %w", err)
	}
	trials := decision.Experiment.Trials()
	if len(trials) != len(expectedLabels)+1 {
		return invalid("incorrect stage candidate count or repetitions")
	}
	for i, label := range expectedLabels {
		if trials[i+1].DriverName() != "zarlcode-harness-"+label+"-r1" {
			return invalid("incorrect stage labels or order")
		}
	}
	return decision, nil
}
