package llm

//go:generate go tool goenums -f pricing_enum.go

// billingMode distinguishes absent billing information from token metering,
// subscription access and unmetered access. Neither latter mode means free tokens.
type billingMode int

const (
	billingUnknown      billingMode = iota // unknown
	billingTokenMetered                    // token-metered
	billingSubscription                    // subscription
	billingUnmetered                       // unmetered
)

// pricingIssueKind describes why selected token pricing cannot cover all usage.
type pricingIssueKind int

const (
	pricingIssueUnknown            pricingIssueKind = iota // unknown
	pricingUnknownBilling                                  // unknown-billing
	pricingUnknownBasis                                    // unknown-basis
	pricingUnresolvedApplicability                         // unresolved-applicability
	pricingUnknownQuantity                                 // unknown-quantity
	pricingUnknownRate                                     // unknown-rate
	pricingInconsistentSubsets                             // inconsistent-subsets
	pricingUnknownWriteLifetime                            // unknown-write-lifetime
	pricingUnsupportedDimensions                           // unsupported-dimensions
	pricingArithmeticOverflow                              // arithmetic-overflow
)
