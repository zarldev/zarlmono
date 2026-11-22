package engine

//go:generate go tool goenums -f target_facts_enum.go

// operationalBudgetSource records only the precision the budget resolver supplies.
type operationalBudgetSource int

const (
	budgetUnknown                operationalBudgetSource = iota // unknown
	budgetExplicitConfiguration                                 // explicit_configuration
	budgetOpaqueLegacyResolution                                // opaque_legacy_resolution
	budgetApplicationFallback                                   // application_fallback
)
