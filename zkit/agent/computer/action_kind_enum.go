package computer

//go:generate go tool goenums -f action_kind_enum.go

// actionKind is the goenums source for ActionKind — the closed set of
// surface actions supported by the stable computer-use wire contract. The
// trailing comment on each constant is the human-readable snake_case wire
// identifier.
type actionKind int

const (
	// navigate loads a new location on the current surface.
	navigate actionKind = iota // navigate
	// click activates a target, typically with a primary pointer click.
	click // click
	// fill enters a value into an editable target.
	fill // fill
	// press sends a key or button press to the surface or target.
	press // press
	// scroll moves the visible viewport by the requested delta.
	scroll // scroll
)
