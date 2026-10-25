package swarm

//go:generate go tool goenums -f state_enum.go

// state is the logical agent lifecycle, not the terminal state of a task receipt.
type state int

const (
	queued    state = iota + 1 // queued
	running                    // running
	idle                       // idle
	suspended                  // suspended
	stopped                    // stopped
)
