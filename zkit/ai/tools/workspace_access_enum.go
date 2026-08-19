package tools

//go:generate go tool goenums -f workspace_access_enum.go

// workspaceAccess is the goenums source for WorkspaceAccess. It classifies the
// workspace coordination capability a tool requires. The zero value is NONE:
// the tool neither reads nor writes the workspace.
type workspaceAccess int

const (
	invalid workspaceAccess = iota // invalid invalid
	// workspaceNone does not access the workspace.
	none // none
	// workspaceRead reads workspace state without changing it. Multiple tasks
	// may hold read access concurrently.
	read // read
	// workspaceWrite can change workspace state. It is exclusive with every
	// other owner's read or write access.
	write // write
)
