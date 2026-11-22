package llm

import "strings"

// MaxExecutionIDBytes bounds each nonempty identifier in Affinity and
// ExecutionObservation, including route, model, deployment and reported-cost
// source/currency identifiers. External codecs must enforce this byte limit and
// reject control characters and secrets before constructing these domain values.
// These fields carry semantic IDs, never URLs, API bases, cache keys, headers or
// arbitrary gateway metadata. An empty observation ID means unknown.
const MaxExecutionIDBytes = 256

// SessionID is an opaque stable identifier supplied and scoped by the host.
// It is not a credential, cache key or observed deployment. The library neither
// generates it nor owns global session pins. See MaxExecutionIDBytes.
type SessionID string

// Affinity carries request placement intent only. Prefer and require use a
// nonempty host-supplied SessionID; none requests no affinity. The session owner
// must reset identity/pins when tenant, route or protocol changes, rather than
// carrying an old placement into a new scope.
//
// Prefer is best effort and never guarantees a platform worker or cache hit.
// An implementation consuming require must reject it before I/O unless it has
// verified enforcement for the selected surface; merely sending a session ID,
// observing a deployment, or receiving advertised support is not enforcement.
// LiteLLM consumes preference as a best-effort hint and rejects unverified require;
// other adapters currently ignore this intent. Complete remains lazy: any
// rejection occurs on stream iteration.
type Affinity struct {
	Mode      AffinityMode
	SessionID SessionID
}

// CallID is an opaque reported call identity, not a tool-call ID or credential.
// See MaxExecutionIDBytes; no identity is inferred from its spelling.
type CallID string

// ModelVersionID is an opaque reported model version, not a canonical model
// identity or a pricing revision. See MaxExecutionIDBytes.
type ModelVersionID string

// GatewayVersionID identifies the gateway software runtime version, not the
// reported model version. See MaxExecutionIDBytes.
type GatewayVersionID string

// ObservedCount distinguishes an unknown event count from a reported zero.
// Known counts are nonnegative; external codecs own scalar validation. Counts
// have no token units and must not be represented by TokenQuantity.
type ObservedCount struct {
	Count int64
	Known bool
}

// ExecutionObservation is bounded execution evidence, not a routing decision,
// capability resolution, invoice or estimate. Its zero value is all unknown.
// IDs are open semantic values subject to MaxExecutionIDBytes at the external
// boundary. No aliases, URLs or deployment IDs establish a serving provider,
// canonical model, currency, pricing basis or affinity guarantee.
//
// Request route/alias, opaque gateway placement and reported model/version/call
// identities remain separate even when a gateway happens to report equal text.
// GatewayVersion identifies gateway software, independently of the serving model
// version; an empty value means the runtime version is unknown.
// Value copies contain no mutable collections; Clone also owns string storage.
type ExecutionObservation struct {
	Route          RouteID
	RequestedModel ModelID

	// GatewayDeployment is the gateway's opaque deployment ID, not an upstream
	// provider or proof that a particular platform worker served the request.
	GatewayDeployment DeploymentID
	GatewayVersion    GatewayVersionID
	ReportedModel     ModelID
	ReportedVersion   ModelVersionID
	GatewayCallID     CallID
	ProviderCallID    CallID

	// Prompt-cache counts are token observations only. Neither these counts nor
	// any cache key establishes a whole-response-cache hit. This contract does
	// not carry raw cache keys or assert whole-response-cache outcomes.
	PromptCacheReadTokens  TokenQuantity
	PromptCacheWriteTokens TokenQuantity
	RetryCount             ObservedCount
	FallbackCount          ObservedCount

	// AffinitySupport is an explicit server feature assertion, not request
	// intent, verified enforcement or evidence that affinity was honored.
	AffinitySupport Support

	// ReportedCost is kept separate from local estimates. Its Reported flag
	// preserves explicit zero; coverage and currency may remain unknown.
	// Never add it to an estimate covering the same usage. No USD is assumed.
	ReportedCost ReportedCost
}

// Clone returns an execution observation with independently owned string storage.
func (o ExecutionObservation) Clone() ExecutionObservation {
	o.Route = RouteID(strings.Clone(string(o.Route)))
	o.RequestedModel = ModelID(strings.Clone(string(o.RequestedModel)))
	o.GatewayDeployment = DeploymentID(strings.Clone(string(o.GatewayDeployment)))
	o.GatewayVersion = GatewayVersionID(strings.Clone(string(o.GatewayVersion)))
	o.ReportedModel = ModelID(strings.Clone(string(o.ReportedModel)))
	o.ReportedVersion = ModelVersionID(strings.Clone(string(o.ReportedVersion)))
	o.GatewayCallID = CallID(strings.Clone(string(o.GatewayCallID)))
	o.ProviderCallID = CallID(strings.Clone(string(o.ProviderCallID)))
	o.ReportedCost.Currency = Currency(strings.Clone(string(o.ReportedCost.Currency)))
	o.ReportedCost.Source = strings.Clone(o.ReportedCost.Source)
	return o
}
