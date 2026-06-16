package profile

import "errors"

// ErrNotFound is returned by admin paths that need exact lookup
// (Registry.Resolve falls back to the default profile instead).
var ErrNotFound = errors.New("profile not found")
