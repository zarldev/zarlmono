package tui

//go:generate go tool goenums -f activity_phase_enum.go

// activityPhase records the last observed streaming phase. A new active turn
// starts at working; zero is reserved for inactive, uninitialized activity.
type activityPhase int

const (
	activityWorking    activityPhase = iota + 1 // working
	activityThinking                            // thinking
	activityResponding                          // responding
)
