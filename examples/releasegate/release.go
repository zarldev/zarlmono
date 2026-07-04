package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

const (
	checkTests        = "tests"
	checkChangelog    = "changelog"
	checkRollbackPlan = "rollback_plan"
	channelProduction = "production"
)

var requiredChecks = []string{checkTests, checkChangelog, checkRollbackPlan}

// Release is the tiny world-state object the tools write and the guardrails /
// oracle read. Keep this object small: only facts that decide policy and goal.
type Release struct {
	mu sync.Mutex

	version       string
	checks        map[string]bool
	evidence      map[string]string
	notes         ReleaseNotes
	notesApproved bool
	published     bool
	channel       string
	events        []string
}

// ReleaseNotes is the written-by-the-agent release documentation the
// notes-quality guardrail inspects before approval.
type ReleaseNotes struct {
	Summary  string
	Risk     string
	Rollback string
}

// Snapshot is a point-in-time copy of the release state, safe to read
// without holding the Release lock. Missing lists the required checks
// that are still red.
type Snapshot struct {
	Version       string
	Checks        map[string]bool
	Evidence      map[string]string
	Notes         ReleaseNotes
	NotesApproved bool
	Published     bool
	Channel       string
	Missing       []string
	Events        []string
}

// NewRelease returns a release with every required check red and
// nothing published.
func NewRelease(version string) *Release {
	checks := make(map[string]bool, len(requiredChecks))
	for _, name := range requiredChecks {
		checks[name] = false
	}
	return &Release{
		version:  version,
		checks:   checks,
		evidence: make(map[string]string),
	}
}

// SetCheck flips a required check, recording the supplied evidence.
// Unknown check names error.
func (r *Release) SetCheck(name string, ok bool, evidence string) error {
	if !slices.Contains(requiredChecks, name) {
		return fmt.Errorf("unknown check %q (allowed: %s)", name, strings.Join(requiredChecks, ", "))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = ok
	r.evidence[name] = evidence
	r.events = append(r.events, fmt.Sprintf("check %s=%t", name, ok))
	return nil
}

// SetNotes stores the release notes and clears any prior approval —
// edited notes must be re-approved.
func (r *Release) SetNotes(notes ReleaseNotes) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = notes
	r.notesApproved = false
	r.events = append(r.events, "notes written")
}

// ApproveNotes marks the current notes as reviewed.
func (r *Release) ApproveNotes() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notesApproved = true
	r.events = append(r.events, "notes approved by guardrail")
}

// Ready reports whether every required check is green and the notes
// are approved — the gate the publish tool enforces.
func (r *Release) Ready() bool {
	s := r.Snapshot()
	return len(s.Missing) == 0
}

// Published reports whether the release has been published — the
// world-state predicate the pursue Goal polls.
func (r *Release) Published() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.published
}

// Publish records the release as published to channel.
func (r *Release) Publish(channel string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.published = true
	r.channel = channel
	r.events = append(r.events, "published to "+channel)
}

// Snapshot returns a copy of the current state for feedback messages
// and per-attempt logging.
func (r *Release) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	checks := make(map[string]bool, len(r.checks))
	maps.Copy(checks, r.checks)
	evidence := make(map[string]string, len(r.evidence))
	maps.Copy(evidence, r.evidence)
	events := append([]string(nil), r.events...)

	var missing []string
	for _, name := range requiredChecks {
		if !checks[name] {
			missing = append(missing, name)
		}
	}
	if !r.notesApproved {
		missing = append(missing, "approved_release_notes")
	}

	return Snapshot{
		Version:       r.version,
		Checks:        checks,
		Evidence:      evidence,
		Notes:         r.notes,
		NotesApproved: r.notesApproved,
		Published:     r.published,
		Channel:       r.channel,
		Missing:       missing,
		Events:        events,
	}
}
