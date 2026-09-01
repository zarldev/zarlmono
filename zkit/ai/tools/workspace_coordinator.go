package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// WorkspaceOwner identifies the task that owns workspace access. It is an
// opaque value so coordination does not depend on runner or spawn packages.
type WorkspaceOwner string

// ErrWorkspaceConflict reports that workspace access is held incompatibly by
// another owner. Callers can recover by waiting or choosing disjoint work.
var ErrWorkspaceConflict = errors.New("workspace access conflict")

// ErrWorkspaceCoordinatorClosed reports acquisition after shutdown starts.
var ErrWorkspaceCoordinatorClosed = errors.New("workspace coordinator is closed")

type workspaceLeaseState struct {
	once        sync.Once
	coordinator *WorkspaceCoordinator
	claimID     uint64
}

// WorkspaceLease represents workspace access held by one owner. Release is
// idempotent so deferred cleanup remains safe after nested control flow.
type WorkspaceLease struct {
	state  *workspaceLeaseState
	access WorkspaceAccess
	paths  []string
}

// Access returns the access capability held by l.
func (l WorkspaceLease) Access() WorkspaceAccess { return l.access }

// Paths returns the normalized workspace-relative subtrees covered by l. A
// single "." path covers the entire workspace.
func (l WorkspaceLease) Paths() []string { return append([]string(nil), l.paths...) }

// Release relinquishes one acquisition represented by l. It is safe to call
// more than once. A zero lease does nothing.
func (l WorkspaceLease) Release() {
	if l.state == nil {
		return
	}
	l.state.once.Do(func() {
		l.state.coordinator.release(l.state.claimID)
	})
}

type workspaceClaim struct {
	owner  WorkspaceOwner
	access WorkspaceAccess
	paths  []string
}

type workspaceWaiter struct {
	id     uint64
	owner  WorkspaceOwner
	access WorkspaceAccess
	paths  []string
}

// WorkspaceCoordinator coordinates path-aware workspace access. Fail-fast
// acquisitions return ErrWorkspaceConflict immediately; waiting acquisitions
// are FIFO among conflicting requests while disjoint requests may bypass them.
type WorkspaceCoordinator struct {
	mu           sync.Mutex
	nextID       uint64
	nextWaiter   uint64
	claims       map[uint64]workspaceClaim
	waiters      []*workspaceWaiter
	changed      chan struct{}
	shuttingDown bool
}

// NewWorkspaceCoordinator creates an empty coordinator for one workspace.
func NewWorkspaceCoordinator() *WorkspaceCoordinator {
	return &WorkspaceCoordinator{
		claims:  make(map[uint64]workspaceClaim),
		changed: make(chan struct{}),
	}
}

// BeginShutdown rejects new acquisitions and wakes queued waiters. Active
// leases remain valid until their owners release them.
func (c *WorkspaceCoordinator) BeginShutdown() {
	c.mu.Lock()
	if !c.shuttingDown {
		c.shuttingDown = true
		c.signalChangedLocked()
	}
	c.mu.Unlock()
}

// Acquire tries to acquire workspace-wide access for owner without waiting.
// NONE produces a no-op lease.
func (c *WorkspaceCoordinator) Acquire(owner WorkspaceOwner, access WorkspaceAccess) (WorkspaceLease, error) {
	return c.AcquirePaths(owner, access, nil)
}

// AcquirePaths tries to acquire access to workspace-relative subtrees. Empty
// paths conservatively mean the entire workspace. Paths are lexical scopes,
// not a filesystem sandbox.
func (c *WorkspaceCoordinator) AcquirePaths(owner WorkspaceOwner, access WorkspaceAccess, paths []string) (WorkspaceLease, error) {
	normalized, err := normalizeWorkspacePaths(paths)
	if err != nil {
		return WorkspaceLease{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shuttingDown {
		return WorkspaceLease{}, ErrWorkspaceCoordinatorClosed
	}
	return c.acquireLocked(owner, access, normalized)
}

// AcquirePathsWait waits until access to workspace-relative subtrees is
// available or ctx is cancelled. Conflicting requests are admitted in arrival
// order; disjoint requests and compatible readers may proceed concurrently.
func (c *WorkspaceCoordinator) AcquirePathsWait(ctx context.Context, owner WorkspaceOwner, access WorkspaceAccess, paths []string) (WorkspaceLease, error) {
	normalized, err := normalizeWorkspacePaths(paths)
	if err != nil {
		return WorkspaceLease{}, err
	}
	if err := validateWorkspaceRequest(owner, access); err != nil {
		return WorkspaceLease{}, err
	}
	if access == WorkspaceAccesses.NONE {
		return WorkspaceLease{access: access, paths: append([]string(nil), normalized...)}, nil
	}

	c.mu.Lock()
	if c.shuttingDown {
		c.mu.Unlock()
		return WorkspaceLease{}, ErrWorkspaceCoordinatorClosed
	}
	c.nextWaiter++
	waiter := &workspaceWaiter{id: c.nextWaiter, owner: owner, access: access, paths: normalized}
	c.waiters = append(c.waiters, waiter)
	if c.waiterCanAcquireLocked(waiter) {
		c.removeWaiterLocked(waiter.id)
		lease := c.grantLocked(owner, access, normalized)
		c.mu.Unlock()
		return lease, nil
	}
	blockers := c.blockersLocked(waiter)
	changed := c.changed
	c.mu.Unlock()

	observer := WorkspaceWaitObserverFromContext(ctx)
	started := time.Now()
	call := workspaceWaitCallFromContext(ctx)
	if observer != nil {
		observer.OnWorkspaceWaitStarted(WorkspaceWaitStarted{Owner: owner, Access: access, Paths: append([]string(nil), normalized...), Blockers: blockers, Call: call, Started: started})
	}
	for {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			if c.removeWaiterLocked(waiter.id) {
				c.signalChangedLocked()
			}
			c.mu.Unlock()
			if observer != nil {
				observer.OnWorkspaceWaitEnded(WorkspaceWaitEnded{Owner: owner, Access: access, Paths: append([]string(nil), normalized...), Call: call, Outcome: WorkspaceWaitOutcomes.WORKSPACEWAITCANCELLED, Waited: time.Since(started)})
			}
			return WorkspaceLease{}, fmt.Errorf("wait for workspace access: %w", ctx.Err())
		case <-changed:
			c.mu.Lock()
			if c.shuttingDown {
				c.removeWaiterLocked(waiter.id)
				c.mu.Unlock()
				if observer != nil {
					observer.OnWorkspaceWaitEnded(WorkspaceWaitEnded{Owner: owner, Access: access, Paths: append([]string(nil), normalized...), Call: call, Outcome: WorkspaceWaitOutcomes.WORKSPACEWAITCANCELLED, Waited: time.Since(started)})
				}
				return WorkspaceLease{}, ErrWorkspaceCoordinatorClosed
			}
			if c.waiterCanAcquireLocked(waiter) {
				c.removeWaiterLocked(waiter.id)
				lease := c.grantLocked(owner, access, normalized)
				c.mu.Unlock()
				if observer != nil {
					observer.OnWorkspaceWaitEnded(WorkspaceWaitEnded{Owner: owner, Access: access, Paths: append([]string(nil), normalized...), Call: call, Outcome: WorkspaceWaitOutcomes.WORKSPACEWAITACQUIRED, Waited: time.Since(started)})
				}
				return lease, nil
			}
			changed = c.changed
			c.mu.Unlock()
		}
	}
}

func (c *WorkspaceCoordinator) acquireLocked(owner WorkspaceOwner, access WorkspaceAccess, paths []string) (WorkspaceLease, error) {
	if err := validateWorkspaceRequest(owner, access); err != nil {
		return WorkspaceLease{}, err
	}
	if access == WorkspaceAccesses.NONE {
		return WorkspaceLease{access: access, paths: append([]string(nil), paths...)}, nil
	}
	for _, claim := range c.claims {
		if requestsConflict(owner, access, paths, claim.owner, claim.access, claim.paths) {
			return WorkspaceLease{}, fmt.Errorf("%w: owner %q holds %s access on %s", ErrWorkspaceConflict, claim.owner, claim.access, strings.Join(claim.paths, ", "))
		}
	}
	return c.grantLocked(owner, access, paths), nil
}

func validateWorkspaceRequest(owner WorkspaceOwner, access WorkspaceAccess) error {
	if !access.IsValid() {
		return fmt.Errorf("workspace access %q is invalid", access)
	}
	if access != WorkspaceAccesses.NONE && owner == "" {
		return errors.New("workspace owner is empty")
	}
	return nil
}

func (c *WorkspaceCoordinator) grantLocked(owner WorkspaceOwner, access WorkspaceAccess, paths []string) WorkspaceLease {
	c.nextID++
	claimID := c.nextID
	claimPaths := append([]string(nil), paths...)
	c.claims[claimID] = workspaceClaim{owner: owner, access: access, paths: claimPaths}
	return WorkspaceLease{
		state:  &workspaceLeaseState{coordinator: c, claimID: claimID},
		access: access,
		paths:  claimPaths,
	}
}

func (c *WorkspaceCoordinator) waiterCanAcquireLocked(waiter *workspaceWaiter) bool {
	for _, claim := range c.claims {
		if requestsConflict(waiter.owner, waiter.access, waiter.paths, claim.owner, claim.access, claim.paths) {
			return false
		}
	}
	for _, earlier := range c.waiters {
		if earlier.id == waiter.id {
			break
		}
		if requestsConflict(waiter.owner, waiter.access, waiter.paths, earlier.owner, earlier.access, earlier.paths) {
			return false
		}
	}
	return true
}

func requestsConflict(leftOwner WorkspaceOwner, leftAccess WorkspaceAccess, leftPaths []string, rightOwner WorkspaceOwner, rightAccess WorkspaceAccess, rightPaths []string) bool {
	if leftOwner == rightOwner || leftAccess == WorkspaceAccesses.READ && rightAccess == WorkspaceAccesses.READ {
		return false
	}
	return scopesOverlap(leftPaths, rightPaths)
}

func (c *WorkspaceCoordinator) removeWaiterLocked(id uint64) bool {
	for index, waiter := range c.waiters {
		if waiter.id == id {
			c.waiters = append(c.waiters[:index], c.waiters[index+1:]...)
			return true
		}
	}
	return false
}

func (c *WorkspaceCoordinator) signalChangedLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *WorkspaceCoordinator) blockersLocked(waiter *workspaceWaiter) []WorkspaceWaitBlocker {
	blockers := make([]WorkspaceWaitBlocker, 0)
	for _, claim := range c.claims {
		if requestsConflict(waiter.owner, waiter.access, waiter.paths, claim.owner, claim.access, claim.paths) {
			blockers = append(blockers, WorkspaceWaitBlocker{Owner: claim.owner, Access: claim.access, Paths: append([]string(nil), claim.paths...)})
		}
	}
	for _, earlier := range c.waiters {
		if earlier.id == waiter.id {
			break
		}
		if requestsConflict(waiter.owner, waiter.access, waiter.paths, earlier.owner, earlier.access, earlier.paths) {
			blockers = append(blockers, WorkspaceWaitBlocker{Owner: earlier.owner, Access: earlier.access, Paths: append([]string(nil), earlier.paths...)})
		}
	}
	return blockers
}

func (c *WorkspaceCoordinator) release(claimID uint64) {
	c.mu.Lock()
	if _, exists := c.claims[claimID]; exists {
		delete(c.claims, claimID)
		c.signalChangedLocked()
	}
	c.mu.Unlock()
}

func normalizeWorkspacePaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return []string{"."}, nil
	}
	normalized := make([]string, 0, len(paths))
	for _, raw := range paths {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, errors.New("workspace path is empty")
		}
		if filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
			return nil, fmt.Errorf("workspace path %q must be relative", raw)
		}
		clean := filepath.Clean(raw)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("workspace path %q escapes the workspace", raw)
		}
		normalized = append(normalized, filepath.ToSlash(clean))
	}
	return compactWorkspacePaths(normalized), nil
}

func compactWorkspacePaths(paths []string) []string {
	sort.Strings(paths)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		covered := false
		for _, existing := range out {
			if scopeContains(existing, path) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, path)
		}
	}
	return out
}

func scopesOverlap(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if scopeContains(a, b) || scopeContains(b, a) {
				return true
			}
		}
	}
	return false
}

func scopeContains(parent, child string) bool {
	return parent == "." || parent == child || strings.HasPrefix(child, parent+"/")
}
