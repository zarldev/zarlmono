package computer

//go:generate go tool goenums -f surface_kind_enum.go

// surfaceKind is the goenums source for SurfaceKind — the closed set of
// surface families observations and actions can address. The trailing comment
// on each constant is the human-readable snake_case wire identifier.
type surfaceKind int

const (
	// browser is a browser surface.
	browser surfaceKind = iota // browser
	// desktop is a desktop surface.
	desktop // desktop
	// terminal is a terminal surface.
	terminal // terminal
)
