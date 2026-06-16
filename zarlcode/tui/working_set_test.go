package tui_test

import (
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/tui"
)

func TestWorkingSetCoalescesFileSummariesAndPreservesMutations(t *testing.T) {
	ws := tui.NewWorkingSet("/repo")
	ws.StartTurn("turn-1")

	firstDiff := "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n-old\n+new"
	secondDiff := "@@ -3,0 +4,1 @@\n+again"
	first := ws.RecordDiff("/repo/foo.go", firstDiff)
	second := ws.RecordDiff("foo.go", secondDiff)

	files := ws.FilesChangedThisSession()
	if len(files) != 1 {
		t.Fatalf("FilesChangedThisSession len = %d, want 1: %#v", len(files), files)
	}
	file := files[0]
	if file.Path != "foo.go" {
		t.Fatalf("file.Path = %q, want foo.go", file.Path)
	}
	if file.Mutations != 2 || file.Additions != 2 || file.Deletions != 1 {
		t.Fatalf("file summary = %+v, want 2 mutations, +2, -1", file)
	}
	if !file.FirstChangedAt.Equal(first.ChangedAt) || !file.LastChangedAt.Equal(second.ChangedAt) {
		t.Fatalf("file timestamps = %s / %s, want %s / %s", file.FirstChangedAt, file.LastChangedAt, first.ChangedAt, second.ChangedAt)
	}

	mutations := ws.MutationsForFile("/repo/foo.go")
	if len(mutations) != 2 {
		t.Fatalf("MutationsForFile len = %d, want 2: %#v", len(mutations), mutations)
	}
	if mutations[0].Diff != firstDiff || mutations[1].Diff != secondDiff {
		t.Fatalf("mutation history did not preserve diffs: %#v", mutations)
	}
	if mutations[0].MutationOrdinal != 1 || mutations[1].MutationOrdinal != 2 {
		t.Fatalf("mutation ordinals = %d/%d, want 1/2", mutations[0].MutationOrdinal, mutations[1].MutationOrdinal)
	}
	for _, mutation := range mutations {
		if mutation.TurnID != "turn-1" || mutation.TurnOrdinal != 1 {
			t.Fatalf("mutation turn = %q/%d, want turn-1/1", mutation.TurnID, mutation.TurnOrdinal)
		}
	}
}

func TestWorkingSetQueriesSessionTurnFileAndMutationHistory(t *testing.T) {
	ws := tui.NewWorkingSet("/repo")

	ws.StartTurn("turn-1")
	ws.RecordDiff("/repo/a.go", "@@\n+a1")
	ws.RecordDiff("/repo/b.go", "@@\n-b1")
	ws.CompleteTurn("turn-1")

	ws.StartTurn("turn-2")
	ws.RecordDiff("/repo/a.go", "@@\n-a1\n+a2")
	ws.CompleteTurn("turn-2")

	if got, want := filePaths(ws.FilesChangedThisSession()), []string{"a.go", "b.go"}; !slices.Equal(got, want) {
		t.Fatalf("session files = %v, want %v", got, want)
	}
	if got, want := filePaths(ws.FilesChangedForTurn("turn-1")), []string{"a.go", "b.go"}; !slices.Equal(got, want) {
		t.Fatalf("turn-1 files = %v, want %v", got, want)
	}
	if got, want := filePaths(ws.FilesChangedForTurn("turn-2")), []string{"a.go"}; !slices.Equal(got, want) {
		t.Fatalf("turn-2 files = %v, want %v", got, want)
	}

	fileMutations := ws.MutationsForFile("a.go")
	if len(fileMutations) != 2 {
		t.Fatalf("a.go mutations len = %d, want 2: %#v", len(fileMutations), fileMutations)
	}
	if fileMutations[0].TurnID != "turn-1" || fileMutations[1].TurnID != "turn-2" {
		t.Fatalf("a.go mutation turns = %q/%q, want turn-1/turn-2", fileMutations[0].TurnID, fileMutations[1].TurnID)
	}
	if fileMutations[0].TurnOrdinal != 1 || fileMutations[1].TurnOrdinal != 2 {
		t.Fatalf("a.go mutation turn ordinals = %d/%d, want 1/2", fileMutations[0].TurnOrdinal, fileMutations[1].TurnOrdinal)
	}

	turnMutations := ws.MutationsForTurn("turn-1")
	if len(turnMutations) != 2 {
		t.Fatalf("turn-1 mutations len = %d, want 2: %#v", len(turnMutations), turnMutations)
	}
	if got, want := []string{turnMutations[0].Path, turnMutations[1].Path}, []string{"a.go", "b.go"}; !slices.Equal(got, want) {
		t.Fatalf("turn-1 mutation paths = %v, want %v", got, want)
	}
}

func TestSessionWorkingSetTracksTopLevelTurn(t *testing.T) {
	s := tui.NewSession("~/repo", "/repo", "main")
	// Top-level turn starts with ordinal 1.
	s.WorkingSet.StartTurn("parent")
	// Depth > 0 (sub-agent) does NOT start a working-set turn.
	s.WorkingSet.RecordDiff("/repo/nested/file.go", "@@\n+line")
	s.WorkingSet.CompleteTurn("parent")
	// Diff after turn complete — no active turn.
	s.WorkingSet.RecordDiff("/repo/after.go", "@@\n+after")

	mutations := s.WorkingSet.MutationsForFile("nested/file.go")
	if len(mutations) != 1 {
		t.Fatalf("nested/file.go mutations len = %d, want 1", len(mutations))
	}
	if mutations[0].TurnID != "parent" || mutations[0].TurnOrdinal != 1 {
		t.Fatalf("nested/file.go turn = %q/%d, want parent/1", mutations[0].TurnID, mutations[0].TurnOrdinal)
	}

	after := s.WorkingSet.MutationsForFile("after.go")
	if len(after) != 1 {
		t.Fatalf("after.go mutations len = %d, want 1", len(after))
	}
	if after[0].TurnID != "" || after[0].TurnOrdinal != 0 {
		t.Fatalf("after.go turn = %q/%d, want no active turn", after[0].TurnID, after[0].TurnOrdinal)
	}
}

func filePaths(files []tui.WorkingSetFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}
