package accountingwire

//go:generate go tool goenums -f session_accounting_enum.go

// estimateAvailability describes only the optional frozen estimate snapshots.
// Its zero value is invalid on the storage boundary.
type estimateAvailability int

const (
	estimateInvalid      estimateAvailability = iota // invalid
	estimateAvailable                                // available
	estimateMissingCard                              // missing_card
	estimateMissingUsage                             // missing_usage
	estimateBothMissing                              // both_missing
	estimateNotComputed                              // not_computed
)
