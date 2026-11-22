package backends

//go:generate go tool goenums -f model_catalog_enum.go

// modelResolutionIssueKind identifies why cached evidence cannot establish a claim.
type modelResolutionIssueKind int

const (
	resolutionUnknown    modelResolutionIssueKind = iota // unknown
	resolutionConflict                                   // conflict
	resolutionStale                                      // stale
	resolutionUndated                                    // undated
	resolutionIncomplete                                 // incomplete
)

// modelEvidenceLayer identifies the owner of a capability assertion.
type modelEvidenceLayer int

const (
	layerUnknown    modelEvidenceLayer = iota // unknown
	layerModel                                // model
	layerDeployment                           // deployment
	layerGateway                              // gateway
	layerClient                               // client
	layerPolicy                               // policy
)
