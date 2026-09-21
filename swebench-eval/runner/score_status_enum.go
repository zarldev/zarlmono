package runner

//go:generate go tool goenums -f score_status_enum.go

// scoreStatus describes evidence about scoring, independently of agent success.
type scoreStatus int

const (
	notRecorded  scoreStatus = iota // not_recorded
	notRequested                    // not_requested
	pending                         // pending
	running                         // running
	completed                       // completed
	unavailable                     // unavailable
	failed                          // failed
	cancelled                       // cancelled
)
