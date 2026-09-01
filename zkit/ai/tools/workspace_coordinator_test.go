package tools_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestWorkspaceScopePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope tools.WorkspaceScope
		args  tools.ToolParameters
		want  []string
	}{
		{name: "root fallback"},
		{name: "argument", scope: tools.WorkspaceScopeArgument("path"), args: tools.ToolParameters{"path": "zkit/agent"}, want: []string{"zkit/agent"}},
		{name: "missing argument falls back", scope: tools.WorkspaceScopeArgument("path")},
		{name: "escaping argument falls back", scope: tools.WorkspaceScopeArgument("path"), args: tools.ToolParameters{"path": "../outside"}},
		{name: "fixed", scope: tools.WorkspaceScopeFixed(".zarlcode/plans"), want: []string{".zarlcode/plans"}},
		{name: "patch all paths", scope: tools.WorkspaceScopePatch("patch"), args: tools.ToolParameters{"patch": "*** Begin Patch\n*** Update File: zkit/a.go\n*** Move to: zkit/b.go\n*** Add File: zarlcode/c.go\n*** End Patch"}, want: []string{"zkit/a.go", "zkit/b.go", "zarlcode/c.go"}},
		{name: "malformed patch falls back", scope: tools.WorkspaceScopePatch("patch"), args: tools.ToolParameters{"patch": "*** Update File: zkit/a.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.scope.Paths(tt.args)
			if len(got) != len(tt.want) {
				t.Fatalf("Paths() = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("Paths() = %#v, want %#v", got, tt.want)
				}
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

func TestWorkspaceCoordinatorBeginShutdown(t *testing.T) {
	coordinator := tools.NewWorkspaceCoordinator()
	holder, err := coordinator.Acquire("holder", tools.WorkspaceAccesses.WRITE)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	observer := &waitObserver{started: make(chan tools.WorkspaceWaitStarted, 1), ended: make(chan tools.WorkspaceWaitEnded, 1)}
	ctx, cancel := context.WithCancel(tools.ContextWithWorkspaceWaitObserver(t.Context(), observer))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		lease, waitErr := coordinator.AcquirePathsWait(ctx, "waiter", tools.WorkspaceAccesses.READ, nil)
		if waitErr == nil {
			lease.Release()
		}
		done <- waitErr
	}()

	select {
	case started := <-observer.started:
		if started.Owner != "waiter" || started.Access != tools.WorkspaceAccesses.READ || started.BlockerCount() != 1 {
			t.Fatalf("started = %#v", started)
		}
	case <-time.After(time.Second):
		t.Fatal("conflicting waiter did not queue")
	}

	coordinator.BeginShutdown()
	select {
	case err := <-done:
		if !errors.Is(err, tools.ErrWorkspaceCoordinatorClosed) {
			t.Fatalf("queued waiter error = %v, want workspace coordinator closed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued waiter did not wake during shutdown")
	}

	if _, err := coordinator.Acquire("after-shutdown", tools.WorkspaceAccesses.WRITE); !errors.Is(err, tools.ErrWorkspaceCoordinatorClosed) {
		t.Fatalf("post-shutdown acquisition error = %v, want workspace coordinator closed", err)
	}
}

func TestWorkspaceWaitObserverReceivesLifecycle(t *testing.T) {
	coordinator := tools.NewWorkspaceCoordinator()
	holder, err := coordinator.Acquire("holder", tools.WorkspaceAccesses.WRITE)
	if err != nil {
		t.Fatal(err)
	}
	observer := &waitObserver{started: make(chan tools.WorkspaceWaitStarted, 1), ended: make(chan tools.WorkspaceWaitEnded, 1)}
	call := tools.WorkspaceWaitCall{ToolID: "edit-call", ToolName: "edit", ParentToolID: "spawn", Sequence: 2}
	ctx := tools.ContextWithWorkspaceWaitCall(tools.ContextWithWorkspaceWaitObserver(t.Context(), observer), call)
	done := make(chan error, 1)
	go func() {
		lease, waitErr := coordinator.AcquirePathsWait(ctx, "waiter", tools.WorkspaceAccesses.WRITE, nil)
		if waitErr == nil {
			lease.Release()
		}
		done <- waitErr
	}()
	started := <-observer.started
	if started.Owner != "waiter" || started.BlockerCount() != 1 || started.Call != call {
		t.Fatalf("started = %#v", started)
	}
	holder.Release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ended := <-observer.ended
	if ended.Outcome != tools.WorkspaceWaitOutcomes.WORKSPACEWAITACQUIRED || ended.Waited < 0 || ended.Call != call {
		t.Fatalf("ended = %#v", ended)
	}
}

type waitObserver struct {
	started chan tools.WorkspaceWaitStarted
	ended   chan tools.WorkspaceWaitEnded
}

func (o *waitObserver) OnWorkspaceWaitStarted(event tools.WorkspaceWaitStarted) { o.started <- event }
func (o *waitObserver) OnWorkspaceWaitEnded(event tools.WorkspaceWaitEnded)     { o.ended <- event }

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
}
func TestWorkspaceCoordinatorPathScopes(t *testing.T) {
	t.Parallel()

	coordinator := tools.NewWorkspaceCoordinator()
	zkitWriter, err := coordinator.AcquirePaths("zkit-writer", tools.WorkspaceAccesses.WRITE, []string{"zkit/agent"})
	if err != nil {
		t.Fatalf("acquire zkit writer: %v", err)
	}
	defer zkitWriter.Release()

	zarlcodeWriter, err := coordinator.AcquirePaths("zarlcode-writer", tools.WorkspaceAccesses.WRITE, []string{"zarlcode/engine"})
	if err != nil {
		t.Fatalf("acquire disjoint writer: %v", err)
	}
	defer zarlcodeWriter.Release()

	if _, err := coordinator.AcquirePaths("overlap", tools.WorkspaceAccesses.WRITE, []string{"zkit"}); !errors.Is(err, tools.ErrWorkspaceConflict) {
		t.Fatalf("acquire ancestor writer: error = %v, want workspace conflict", err)
	}
	if _, err := coordinator.Acquire("root", tools.WorkspaceAccesses.READ); !errors.Is(err, tools.ErrWorkspaceConflict) {
		t.Fatalf("acquire workspace reader: error = %v, want workspace conflict", err)
	}

	paths := zkitWriter.Paths()
	if len(paths) != 1 || paths[0] != "zkit/agent" {
		t.Fatalf("normalized paths = %#v, want [zkit/agent]", paths)
	}
}

func TestWorkspaceCoordinatorNormalizesAndValidatesPaths(t *testing.T) {
	t.Parallel()

	coordinator := tools.NewWorkspaceCoordinator()
	lease, err := coordinator.AcquirePaths("writer", tools.WorkspaceAccesses.WRITE, []string{"./zkit/agent/../agent", "zkit/agent/tools"})
	if err != nil {
		t.Fatalf("acquire normalized paths: %v", err)
	}
	defer lease.Release()
	if paths := lease.Paths(); len(paths) != 1 || paths[0] != "zkit/agent" {
		t.Fatalf("paths = %#v, want compacted [zkit/agent]", paths)
	}

	for _, path := range []string{"", "../outside"} {
		if _, err := coordinator.AcquirePaths("invalid", tools.WorkspaceAccesses.READ, []string{path}); err == nil {
			t.Fatalf("AcquirePaths(%q) succeeded", path)
		}
	}
}

func TestWorkspaceLeaseCopiedReleaseIsIdempotent(t *testing.T) {
	t.Parallel()

	coordinator := tools.NewWorkspaceCoordinator()
	lease, err := coordinator.AcquirePaths("child", tools.WorkspaceAccesses.WRITE, []string{"zkit"})
	if err != nil {
		t.Fatal(err)
	}
	copyOfLease := lease
	lease.Release()
	copyOfLease.Release()

	other, err := coordinator.AcquirePaths("other", tools.WorkspaceAccesses.WRITE, []string{"zkit"})
	if err != nil {
		t.Fatalf("acquire after copied release: %v", err)
	}
	other.Release()
}

func TestWorkspaceCoordinatorWaitsAndAllowsDisjointBypass(t *testing.T) {
	t.Parallel()

	coordinator := tools.NewWorkspaceCoordinator()
	holder, err := coordinator.AcquirePaths("holder", tools.WorkspaceAccesses.WRITE, []string{"zkit"})
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan tools.WorkspaceLease, 1)
	go func() {
		lease, waitErr := coordinator.AcquirePathsWait(t.Context(), "waiter", tools.WorkspaceAccesses.WRITE, []string{"zkit/agent"})
		if waitErr == nil {
			acquired <- lease
		}
	}()
	select {
	case lease := <-acquired:
		lease.Release()
		t.Fatal("overlapping waiter acquired before release")
	case <-time.After(20 * time.Millisecond):
	}

	disjoint, err := coordinator.AcquirePathsWait(t.Context(), "disjoint", tools.WorkspaceAccesses.WRITE, []string{"zarlcode"})
	if err != nil {
		t.Fatalf("disjoint acquire: %v", err)
	}
	disjoint.Release()
	holder.Release()
	select {
	case lease := <-acquired:
		lease.Release()
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire after release")
	}
}

func TestWorkspaceCoordinatorWaitCancellationCleansQueue(t *testing.T) {
	t.Parallel()

	coordinator := tools.NewWorkspaceCoordinator()
	holder, err := coordinator.AcquirePaths("holder", tools.WorkspaceAccesses.WRITE, []string{"zkit"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := coordinator.AcquirePathsWait(ctx, "cancelled", tools.WorkspaceAccesses.WRITE, []string{"zkit"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait error = %v", err)
	}
	holder.Release()
	lease, err := coordinator.AcquirePathsWait(t.Context(), "next", tools.WorkspaceAccesses.WRITE, []string{"zkit"})
	if err != nil {
		t.Fatalf("acquire after cancellation: %v", err)
	}
	lease.Release()
}

func TestWorkspaceCoordinatorWriterFairness(t *testing.T) {
	t.Parallel()

	coordinator := tools.NewWorkspaceCoordinator()
	holder, err := coordinator.AcquirePaths("holder", tools.WorkspaceAccesses.WRITE, []string{"zkit"})
	if err != nil {
		t.Fatal(err)
	}
	type acquisition struct {
		owner string
		err   error
	}
	order := make(chan acquisition, 2)
	releaseWriter := make(chan struct{})
	observer := &waitObserver{started: make(chan tools.WorkspaceWaitStarted, 1), ended: make(chan tools.WorkspaceWaitEnded, 1)}
	ctx := tools.ContextWithWorkspaceWaitObserver(t.Context(), observer)
	go func() {
		lease, waitErr := coordinator.AcquirePathsWait(ctx, "writer", tools.WorkspaceAccesses.WRITE, []string{"zkit"})
		if waitErr != nil {
			order <- acquisition{owner: "writer", err: waitErr}
			return
		}
		order <- acquisition{owner: "writer"}
		<-releaseWriter
		lease.Release()
	}()
	started := <-observer.started
	if started.Owner != "writer" {
		t.Fatalf("started owner = %q, want writer", started.Owner)
	}
	go func() {
		lease, waitErr := coordinator.AcquirePathsWait(t.Context(), "reader", tools.WorkspaceAccesses.READ, []string{"zkit"})
		if waitErr != nil {
			order <- acquisition{owner: "reader", err: waitErr}
			return
		}
		order <- acquisition{owner: "reader"}
		lease.Release()
	}()
	holder.Release()
	first := <-order
	close(releaseWriter)
	if first.err != nil {
		t.Fatalf("first acquisition error: %v", first.err)
	}
	if first.owner != "writer" {
		t.Fatalf("first acquisition = %q, want writer", first.owner)
	}
	second := <-order
	if second.err != nil {
		t.Fatalf("second acquisition error: %v", second.err)
	}
	if second.owner != "reader" {
		t.Fatalf("second acquisition = %q, want reader", second.owner)
	}
}
