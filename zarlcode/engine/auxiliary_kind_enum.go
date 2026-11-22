package engine

//go:generate go tool goenums -f auxiliary_kind_enum.go

// auxiliaryKind identifies the two latest-only auxiliary inference roles.
type auxiliaryKind int

const (
	none         auxiliaryKind = iota // invalid none
	judge                             // judge
	spawnPlanner                      // spawn_planner
)
