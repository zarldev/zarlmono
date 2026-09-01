package tools

//go:generate go tool goenums -f workspace_scope_enum.go

// workspaceScopeKind is the goenums source for WorkspaceScopeKind. It identifies
// how one tool call derives workspace-relative coordination paths.
type workspaceScopeKind int

const (
	root workspaceScopeKind = iota // root
	// argument reads one relative path from a named argument.
	argument // argument
	// fixed uses host-declared relative paths.
	fixed // fixed
	// patch reads every touched path from a Codex-style patch.
	patch // patch
)
