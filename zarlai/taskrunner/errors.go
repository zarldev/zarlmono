package taskrunner

import "errors"

// ErrProfileNoTools is returned by ProfileRegistry.Resolve when a profile's
// filtered tool set is empty — the task cannot make progress.
var ErrProfileNoTools = errors.New("profile has no tools after filtering")
