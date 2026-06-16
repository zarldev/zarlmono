package pursue

import "github.com/zarldev/zarlmono/zkit/agent/runner"

// LabelAttempt returns a short human-readable phrase describing the
// attempt's outcome: "goal met" when the Decision is Done, otherwise a
// terse description of the terminal reason (e.g. "model stopped, goal
// not met", "hit iteration cap, goal not met", "run error").
func LabelAttempt(report AttemptReport) string {
	if report.Decision.Done {
		return "goal met"
	}
	switch report.Attempt.Result.Reason {
	case runner.TerminalCompleted:
		return "model stopped, goal not met"
	case runner.TerminalMaxIterations:
		return "hit iteration cap, goal not met"
	case runner.TerminalError:
		return "run error"
	default:
		return string(report.Attempt.Result.Reason) + ", goal not met"
	}
}
