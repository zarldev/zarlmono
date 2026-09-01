package tools

//go:generate go tool goenums -f workspace_wait_outcome_enum.go

// workspaceWaitOutcome is the goenums source for WorkspaceWaitOutcome.
type workspaceWaitOutcome int

const (
	workspaceWaitAcquired  workspaceWaitOutcome = iota // acquired
	workspaceWaitCancelled                             // cancelled
)
