package engine

//go:generate go tool goenums -f auxiliary_accounting_purpose_enum.go

// auxiliaryAccountingPurpose identifies an invoked recorded-turn auxiliary stream role.
type auxiliaryAccountingPurpose int

const (
	purposeNone         auxiliaryAccountingPurpose = iota // invalid none
	purposeJudge                                          // judge
	purposeSpawnPlanner                                   // spawn_planner
	purposeSummary                                        // summary
	purposeExecutive                                      // executive
	purposeHandover                                       // handover
)
