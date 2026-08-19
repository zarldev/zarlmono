package prefs

//go:generate go tool goenums -f scope_enum.go

type scope int

const (
	invalid   scope = iota // invalid invalid
	workspace              // workspace
	global                 // global
	effective              // effective
)
