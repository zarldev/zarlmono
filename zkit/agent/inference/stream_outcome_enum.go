package inference

//go:generate go tool goenums -f stream_outcome_enum.go

// streamOutcome describes consumption of an admitted invocation, independently
// of whether a helper can parse or validate the response.
type streamOutcome int

const (
	none        streamOutcome = iota // invalid none
	completed                        // completed
	failed                           // failed
	cancelled                        // cancelled
	stopped                          // stopped
	interrupted                      // interrupted
)
