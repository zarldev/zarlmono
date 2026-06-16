package pursue

//go:generate go tool goenums -f status_enum.go

// status is the goenums source for Status — the terminal classification
// returned by Drive. The trailing comment on each constant is the stable
// wire/log identifier.
type status int

const (
	// succeeded: the Goal reported the task complete.
	succeeded status = iota // succeeded
	// gaveUp: the attempt budget was exhausted without the Goal met.
	gaveUp // gave_up
	// errored: an attempt produced a TerminalError TaskResult (or a watched
	// attempt failed to drain after cancellation).
	errored // errored
)
