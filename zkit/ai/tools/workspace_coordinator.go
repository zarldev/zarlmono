package tools

import (
	"errors"
	"fmt"
	"sync"
)

// WorkspaceOwner identifies the task that owns workspace access. It is an
// opaque value so coordination does not depend on runner or spawn packages.
type WorkspaceOwner string

// ErrWorkspaceConflict reports that workspace access is held incompatibly by
// another owner. Callers can recover by completing later or choosing work that
// does not require the workspace.
var ErrWorkspaceConflict = errors.New("workspace access conflict")

// WorkspaceLease represents workspace access held by one owner. Release is
// idempotent so deferred cleanup remains safe after nested control flow.
type WorkspaceLease struct {
	coordinator *WorkspaceCoordinator
	owner       WorkspaceOwner
	access      WorkspaceAccess
}

// Access returns the access capability held by l.
func (l WorkspaceLease) Access() WorkspaceAccess { return l.access }

// Release relinquishes one acquisition represented by l. It is safe to call
// more than once. A zero lease does nothing.
func (l WorkspaceLease) Release() {
	if l.coordinator != nil {
		l.coordinator.release(l.owner, l.access)
	}
}

// WorkspaceCoordinator coordinates access to one workspace. It never blocks:
// conflicting requests return ErrWorkspaceConflict immediately. The same owner
// may reenter any compatible access; a writer owner may also read while it
// holds the write lease.
type WorkspaceCoordinator struct {
	mu      sync.Mutex
	readers map[WorkspaceOwner]int
	writer  WorkspaceOwner
	writes  int
}

// NewWorkspaceCoordinator creates an empty coordinator for one workspace.
func NewWorkspaceCoordinator() *WorkspaceCoordinator {
	return &WorkspaceCoordinator{readers: make(map[WorkspaceOwner]int)}
}

// Acquire tries to acquire access for owner without waiting. NONE produces a
// no-op lease. An empty owner is invalid because ownership is required for
// conflict detection and reentry.
func (c *WorkspaceCoordinator) Acquire(owner WorkspaceOwner, access WorkspaceAccess) (WorkspaceLease, error) {
	if !access.IsValid() {
		return WorkspaceLease{}, fmt.Errorf("workspace access %q is invalid", access)
	}
	if access == WorkspaceAccesses.NONE {
		return WorkspaceLease{access: access}, nil
	}
	if owner == "" {
		return WorkspaceLease{}, errors.New("workspace owner is empty")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch access {
	case WorkspaceAccesses.READ:
		if c.writer != "" && c.writer != owner {
			return WorkspaceLease{}, fmt.Errorf("%w: writer %q holds workspace", ErrWorkspaceConflict, c.writer)
		}
		c.readers[owner]++
	case WorkspaceAccesses.WRITE:
		if c.writer != "" && c.writer != owner {
			return WorkspaceLease{}, fmt.Errorf("%w: writer %q holds workspace", ErrWorkspaceConflict, c.writer)
		}
		if c.writer == "" && c.hasOtherReader(owner) {
			return WorkspaceLease{}, fmt.Errorf("%w: another owner holds workspace read access", ErrWorkspaceConflict)
		}
		c.writer = owner
		c.writes++
	}

	return WorkspaceLease{coordinator: c, owner: owner, access: access}, nil
}

func (c *WorkspaceCoordinator) hasOtherReader(owner WorkspaceOwner) bool {
	for reader, count := range c.readers {
		if reader != owner && count > 0 {
			return true
		}
	}
	return false
}

func (c *WorkspaceCoordinator) release(owner WorkspaceOwner, access WorkspaceAccess) {
	if access == WorkspaceAccesses.NONE || owner == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	switch access {
	case WorkspaceAccesses.READ:
		if count := c.readers[owner]; count <= 1 {
			delete(c.readers, owner)
		} else {
			c.readers[owner] = count - 1
		}
	case WorkspaceAccesses.WRITE:
		if c.writer != owner || c.writes == 0 {
			return
		}
		c.writes--
		if c.writes == 0 {
			c.writer = ""
		}
	}
}
