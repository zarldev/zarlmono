package tools_test

import (
	"errors"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

func TestToolSpecAccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec tools.ToolSpec
		want tools.WorkspaceAccess
	}{
		{name: "default none", want: tools.WorkspaceAccesses.NONE},
		{name: "legacy mutation writes", spec: tools.ToolSpec{Mutates: true}, want: tools.WorkspaceAccesses.WRITE},
		{name: "legacy workspace effect writes", spec: tools.ToolSpec{AffectsWorkspace: true}, want: tools.WorkspaceAccesses.WRITE},
		{name: "explicit read", spec: tools.ToolSpec{WorkspaceAccess: tools.WorkspaceAccesses.READ}, want: tools.WorkspaceAccesses.READ},
		{name: "explicit none overrides legacy", spec: tools.ToolSpec{Mutates: true, WorkspaceAccess: tools.WorkspaceAccesses.NONE}, want: tools.WorkspaceAccesses.NONE},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.spec.Access(); got != tt.want {
				t.Errorf("Access() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestWorkspaceCoordinator(t *testing.T) {
	t.Parallel()

	coordinator := tools.NewWorkspaceCoordinator()
	readerOne, err := coordinator.Acquire("reader-one", tools.WorkspaceAccesses.READ)
	if err != nil {
		t.Fatalf("acquire first reader: %v", err)
	}
	defer readerOne.Release()

	readerTwo, err := coordinator.Acquire("reader-two", tools.WorkspaceAccesses.READ)
	if err != nil {
		t.Fatalf("acquire concurrent reader: %v", err)
	}
	defer readerTwo.Release()

	if _, err := coordinator.Acquire("writer", tools.WorkspaceAccesses.WRITE); !errors.Is(err, tools.ErrWorkspaceConflict) {
		t.Fatalf("acquire writer with readers: error = %v, want workspace conflict", err)
	}

	readerTwo.Release()
	readerOne.Release()
	writer, err := coordinator.Acquire("writer", tools.WorkspaceAccesses.WRITE)
	if err != nil {
		t.Fatalf("acquire writer after readers release: %v", err)
	}
	defer writer.Release()

	writerAgain, err := coordinator.Acquire("writer", tools.WorkspaceAccesses.WRITE)
	if err != nil {
		t.Fatalf("reenter writer: %v", err)
	}
	defer writerAgain.Release()

	readerByWriter, err := coordinator.Acquire("writer", tools.WorkspaceAccesses.READ)
	if err != nil {
		t.Fatalf("writer read reentry: %v", err)
	}
	defer readerByWriter.Release()

	if _, err := coordinator.Acquire("reader-three", tools.WorkspaceAccesses.READ); !errors.Is(err, tools.ErrWorkspaceConflict) {
		t.Fatalf("acquire reader with writer: error = %v, want workspace conflict", err)
	}

	writerAgain.Release()
	writer.Release()
	readerByWriter.Release()
	if _, err := coordinator.Acquire("reader-three", tools.WorkspaceAccesses.READ); err != nil {
		t.Fatalf("acquire reader after writer release: %v", err)
	}
}

func TestWorkspaceCoordinatorNoneAndValidation(t *testing.T) {
	t.Parallel()

	coordinator := tools.NewWorkspaceCoordinator()
	lease, err := coordinator.Acquire("", tools.WorkspaceAccesses.NONE)
	if err != nil {
		t.Fatalf("acquire none: %v", err)
	}
	if got := lease.Access(); got != tools.WorkspaceAccesses.NONE {
		t.Errorf("none lease access = %s, want NONE", got)
	}
	lease.Release()

	if _, err := coordinator.Acquire("", tools.WorkspaceAccesses.READ); err == nil {
		t.Fatal("acquire read with empty owner succeeded")
	}
}
