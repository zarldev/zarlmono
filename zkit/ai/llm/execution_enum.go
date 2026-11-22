package llm

//go:generate go tool goenums -f execution_enum.go

// affinityMode describes request intent, not server capability or an observed
// placement. None is the natural valid zero: existing requests ask for no pin.
type affinityMode int

const (
	affinityNone    affinityMode = iota // none
	affinityPrefer                      // prefer
	affinityRequire                     // require
)
