package main_test

import (
	"strings"
	"testing"

	experiment "github.com/zarldev/zarlmono/examples/swarm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const validIPv4 = `^(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}$`

func TestRegexOracleAndSubmissionBoundary(t *testing.T) {
	artifact := experiment.NewRegexArtifact()
	original := artifact.Snapshot()
	baseline := artifact.Test()
	if baseline.Passed == baseline.Total || len(baseline.Failures) == 0 {
		t.Fatal("oracle accepted the deliberately faulty baseline")
	}
	for _, pattern := range []string{"", "[", strings.Repeat("x", 4097), original.Pattern} {
		if _, err := artifact.Submit(pattern); err == nil {
			t.Errorf("accepted invalid or unchanged pattern length=%d", len(pattern))
		}
		if artifact.Snapshot() != original {
			t.Fatal("rejected submission mutated the artifact")
		}
	}
	candidate, err := artifact.Submit(validIPv4)
	if err != nil {
		t.Fatal(err)
	}
	result := artifact.Test()
	if candidate.Version != 2 || result.Version != 2 || result.Total < 1200 || result.Passed != result.Total {
		t.Fatalf("candidate=%+v oracle=%+v", candidate, result)
	}
	candidate.Pattern = "caller-owned change"
	if artifact.Snapshot().Pattern != validIPv4 {
		t.Fatal("snapshot aliases artifact state")
	}
	if _, err := artifact.Submit(`^.*$`); err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.Submit(validIPv4); err == nil {
		t.Fatal("third edit exceeded submission limit")
	}
	if result := artifact.Test(); result.Passed == result.Total {
		t.Fatal("oracle trusted an arbitrary replacement")
	}
}

func TestArtifactRoleAuthorityAndVersionBoundReview(t *testing.T) {
	artifact := experiment.NewRegexArtifact()
	for _, role := range []string{"researcher", "implementer", "reviewer"} {
		registry := tools.NewRegistry()
		if err := artifact.Register(registry, role); err != nil {
			t.Fatal(err)
		}
		_, submit := registry.Tool("candidate_submit")
		_, review := registry.Tool("candidate_review")
		if submit != (role == "implementer") || review != (role == "reviewer") {
			t.Fatalf("role %s: submit=%t review=%t", role, submit, review)
		}
		for _, spec := range registry.ToolSpecs() {
			if spec.WorkspaceAccess != tools.WorkspaceAccesses.NONE {
				t.Errorf("artifact granted workspace authority: %+v", spec)
			}
		}
	}
	registry := tools.NewRegistry()
	if err := artifact.Register(registry, "reviewer"); err != nil {
		t.Fatal(err)
	}
	tool, _ := registry.Tool("candidate_review")
	if _, err := artifact.Submit(validIPv4); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		result, err := tool.Execute(t.Context(), tools.ToolCall{ID: "review", Arguments: tools.ToolParameters{"version": version, "approved": true}})
		if err != nil || result.Success != (version == 2) {
			t.Fatalf("version %d: result=%+v err=%v", version, result, err)
		}
	}
	if _, err := artifact.Submit(`^.*$`); err != nil {
		t.Fatal(err)
	}
	if artifact.Snapshot().Approved || artifact.Snapshot().ReviewedVersion != 0 {
		t.Fatal("edit retained approval of an older candidate")
	}
}
