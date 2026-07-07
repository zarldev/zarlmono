package computer

//go:generate go tool goenums -f trigger_kind_enum.go

// triggerKind is the goenums source for TriggerKind — the closed set of
// temporal conditions used by When and Until semantics. The trailing comment
// on each constant is the human-readable snake_case wire identifier.
type triggerKind int

const (
	// visible requires a target to be visible.
	visible triggerKind = iota // visible
	// hidden requires a target to be hidden.
	hidden // hidden
	// focused requires a target to hold focus.
	focused // focused
	// textPresent requires matching text to be present on the surface or target.
	textPresent // text_present
	// valueEquals requires a target value to equal the requested value.
	valueEquals // value_equals
	// urlMatches requires the current surface URL to match the requested value.
	urlMatches // url_matches
	// navigationComplete requires the current navigation to have completed.
	navigationComplete // navigation_complete
	// surfaceStable requires the current surface to have settled.
	surfaceStable // surface_stable
)
