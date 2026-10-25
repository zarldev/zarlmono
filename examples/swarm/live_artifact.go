package main

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"sync"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const initialPattern = `^[0-9]{1,3}(\.[0-9]{1,3}){3}$`

// Candidate is an immutable view of the isolated regex artifact and its review.
type Candidate struct {
	Pattern         string `json:"pattern"`
	Version         int    `json:"version"`
	ReviewedVersion int    `json:"reviewed_version"`
	Approved        bool   `json:"approved"`
}

// OracleResult is an independent comparison against canonical netip IPv4 parsing.
// Failures are capped to keep tool output bounded; Total and Passed are exact.
type OracleResult struct {
	Version  int      `json:"version"`
	Total    int      `json:"total"`
	Passed   int      `json:"passed"`
	Failures []string `json:"failures,omitempty"`
}

// RegexArtifact owns the in-memory candidate. No model-selected code is executed.
type RegexArtifact struct {
	mu        sync.Mutex
	candidate Candidate
}

// NewRegexArtifact creates a deliberately faulty IPv4 validator for the trial.
func NewRegexArtifact() *RegexArtifact {
	return &RegexArtifact{candidate: Candidate{Pattern: initialPattern, Version: 1}}
}

// Snapshot returns a value copy, independent of later submissions and reviews.
func (a *RegexArtifact) Snapshot() Candidate {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.candidate
}

// Submit validates model text at the tool boundary and permits at most two edits.
// Compiling RE2 does not grant filesystem, process, or network authority.
func (a *RegexArtifact) Submit(pattern string) (Candidate, error) {
	if len(pattern) == 0 || len(pattern) > 4096 {
		return Candidate{}, tools.Validation("candidate_submit", "pattern must contain 1–4096 bytes")
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return Candidate{}, tools.Validation("candidate_submit", "pattern must compile as Go RE2")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.candidate.Version >= 3 {
		return Candidate{}, tools.Budget("candidate_submit", "two-edit limit reached")
	}
	if pattern == a.candidate.Pattern {
		return Candidate{}, tools.Validation("candidate_submit", "candidate is unchanged")
	}
	a.candidate = Candidate{Pattern: pattern, Version: a.candidate.Version + 1}
	return a.candidate, nil
}

func (a *RegexArtifact) review(version int, approved bool) (Candidate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if version != a.candidate.Version || version == 1 {
		return Candidate{}, tools.Validation("candidate_review", "review must name the current edited version")
	}
	a.candidate.ReviewedVersion = version
	a.candidate.Approved = approved
	return a.candidate, nil
}

// Test compares the current candidate against boundary, malformed, and ordinary
// cases using a separate standard-library parser, not a model's claims.
func (a *RegexArtifact) Test() OracleResult {
	candidate := a.Snapshot()
	re := regexp.MustCompile(candidate.Pattern) // Submit and the initial constant establish compilation.
	result := OracleResult{Version: candidate.Version}
	check := func(input string) {
		addr, err := netip.ParseAddr(input)
		want := err == nil && addr.Is4() && addr.String() == input
		result.Total++
		if re.MatchString(input) == want {
			result.Passed++
		} else if len(result.Failures) < 12 {
			result.Failures = append(result.Failures, fmt.Sprintf("%q: want match=%t", input, want))
		}
	}
	for _, input := range []string{"", "1.2.3", "1.2.3.4.5", "1..3.4", ".1.2.3.4", "1.2.3.4.",
		" 1.2.3.4", "1.2.3.4 ", "1.2.3.4\n", "\n1.2.3.4", "x1.2.3.4", "1.2.3.4x", "::ffff:1.2.3.4",
		"１.2.3.4", "١.2.3.4", "0.0.0.0", "255.255.255.255", "127.0.0.1", "192.168.1.2", "10.20.30.40"} {
		check(input)
	}
	for position := range 4 {
		for value := range 301 {
			parts := [4]string{"123", "45", "67", "89"}
			parts[position] = strconv.Itoa(value)
			check(fmt.Sprintf("%s.%s.%s.%s", parts[0], parts[1], parts[2], parts[3]))
		}
		for _, invalid := range []string{"00", "01", "001", "0255", "000", "1000", "-1", "+1", "1e2", "0x10"} {
			parts := [4]string{"1", "2", "3", "4"}
			parts[position] = invalid
			check(fmt.Sprintf("%s.%s.%s.%s", parts[0], parts[1], parts[2], parts[3]))
		}
	}
	return result
}

// Register exposes inspect/test to every role, submit only to the implementer,
// and version-bound review only to the reviewer. Role is trusted host wiring.
func (a *RegexArtifact) Register(registry *tools.Registry, role string) error {
	type empty struct{}
	definitions := []tools.Tool{
		tools.New(tools.ToolSpec{Name: "candidate_read", Description: "Read the current isolated IPv4 regex and version.", WorkspaceAccess: tools.WorkspaceAccesses.NONE,
			Parameters: tools.SchemaFor[empty]()}, func(context.Context, empty) (Candidate, error) { return a.Snapshot(), nil }),
		tools.New(tools.ToolSpec{Name: "candidate_test", Description: "Run the fixed independent canonical IPv4 tests on the current regex. No shell or generated code runs.", WorkspaceAccess: tools.WorkspaceAccesses.NONE,
			Parameters: tools.SchemaFor[empty]()}, func(context.Context, empty) (OracleResult, error) { return a.Test(), nil }),
	}
	if role == "implementer" {
		type submitArgs struct {
			Pattern string `json:"pattern" doc:"Replacement Go RE2 regex, at most 4096 bytes."`
		}
		definitions = append(definitions, tools.New(tools.ToolSpec{Name: "candidate_submit", Description: "Replace the isolated regex. At most two edits. No repository file is changed.",
			WorkspaceAccess: tools.WorkspaceAccesses.NONE, DispatchBarrier: true, Parameters: tools.SchemaFor[submitArgs]()},
			func(_ context.Context, args submitArgs) (Candidate, error) { return a.Submit(args.Pattern) }))
	}
	if role == "reviewer" {
		type reviewArgs struct {
			Version  int  `json:"version" doc:"Current edited candidate version you inspected and tested."`
			Approved bool `json:"approved" doc:"Whether your independent review approves this version."`
		}
		definitions = append(definitions, tools.New(tools.ToolSpec{Name: "candidate_review", Description: "Record your verdict bound to the current edited version. Does not replace the host's independent tests.",
			WorkspaceAccess: tools.WorkspaceAccesses.NONE, DispatchBarrier: true, Parameters: tools.SchemaFor[reviewArgs]()},
			func(_ context.Context, args reviewArgs) (Candidate, error) {
				return a.review(args.Version, args.Approved)
			}))
	}
	for _, tool := range definitions {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("register artifact tool: %w", err)
		}
	}
	return nil
}
