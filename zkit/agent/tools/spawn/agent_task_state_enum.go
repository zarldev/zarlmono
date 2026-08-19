package spawn

//go:generate go tool goenums -f agent_task_state_enum.go

// agentTaskState is the goenums source for AgentTaskState. A task is inserted
// as RUNNING before its goroutine starts and transitions exactly once.
type agentTaskState int

const (
	running   agentTaskState = iota + 1 // running
	completed                           // completed
	failed                              // failed
	cancelled                           // cancelled
)
