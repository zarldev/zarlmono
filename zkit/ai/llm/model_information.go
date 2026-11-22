package llm

import (
	"maps"
	"slices"
	"time"
)

// RouteID identifies a configured route, not an upstream provider or URL.
// It is open-ended and need not be an LLMProvider name.
type RouteID string

// ModelID is an opaque requested alias or known canonical model identity.
// Its spelling conveys no capability, protocol, limit, or pricing information.
type ModelID string

// ServingProviderID identifies the known upstream serving organization or
// service. It is open-ended, unlike the closed LLMProvider adapter vocabulary.
type ServingProviderID string

// DeploymentID identifies a known upstream deployment, not a requested alias.
type DeploymentID string

// ModelDescriptor describes a route and requested alias with whatever resolved
// identity and effective inference metadata are known. Empty resolved identity
// fields mean unknown; requested aliases are never rewritten or interpreted.
//
// Surfaces describes what the configured client, gateway (if any), deployment,
// and model together support for each protocol. Native catalogue capabilities
// alone cannot establish effective support. An absent map entry means unknown,
// not unsupported. Evidence records the inputs to this resolved snapshot; it
// does not make any support assertion by itself.
//
// This is a pure domain value, not a wire or persistence schema. Maps and slices
// are caller-owned; value copies borrow them. Use Clone when retaining a snapshot
// independently of the caller. There is no I/O or automatic refresh.
type ModelDescriptor struct {
	Route           RouteID
	RequestedModel  ModelID
	CanonicalModel  ModelID
	ServingProvider ServingProviderID
	Deployment      DeploymentID
	Surfaces        map[ModelProtocol]ModelSurface
	Evidence        []MetadataProvenance
	RateCard        RateCardReference
}

// Clone returns an independently owned snapshot of the descriptor's collections.
func (d ModelDescriptor) Clone() ModelDescriptor {
	d.Surfaces = maps.Clone(d.Surfaces)
	d.Evidence = slices.Clone(d.Evidence)
	return d
}

// ModelSurface describes effective support for one protocol on a resolved route.
// Support reports whether inference through that protocol is supported. Feature
// assertions and limits are independent evidence, not defaults inferred from it:
// protocol support does not imply support for any particular feature.
// The zero value carries only unknown assertions and absent limits.
type ModelSurface struct {
	Support      Support
	Capabilities EffectiveCapabilities
	Modalities   DirectionalModalities
	Limits       ModelLimits
}

// EffectiveCapabilities records effective feature support on a protocol surface.
// Unknown differs from an explicit unsupported assertion. Thinking means a
// reasoning request control; it does not promise displayable or replayable
// reasoning, which are separate assertions.
type EffectiveCapabilities struct {
	Streaming       Support
	Tools           Support
	SystemMessages  Support
	JSONObject      Support
	JSONSchema      Support
	Thinking        Support
	ReasoningOutput Support
	ReasoningReplay Support
	PromptCaching   Support
}

// DirectionalModalities keeps accepted input separate from generated output.
// For example, image input support says nothing about image generation.
type DirectionalModalities struct {
	Input  ModalitySupport
	Output ModalitySupport
}

// ModalitySupport records assertions for the modalities represented by
// ContentPart. These fields are metadata, not an alternate content discriminator;
// providers continue to use ContentPartType on messages.
type ModalitySupport struct {
	Text  Support
	Image Support
	Audio Support
	Video Support
}

// ModelLimits records effective token ceilings for a protocol surface. Zero
// means not known, never unlimited or a reported zero allowance. Positive values
// are independent ceilings; no missing ceiling is inferred from another.
// ContextTokens is the combined input/output window, not an extra allowance.
type ModelLimits struct {
	InputTokens   int64
	OutputTokens  int64
	ContextTokens int64
}

// MetadataProvenance identifies an input snapshot used to resolve a descriptor.
// Snapshot and Revision are opaque, non-secret identifiers, not URLs or raw
// gateway payloads. Different sources can be recorded as separate evidence.
// ObservedAt is when that snapshot was obtained; ExpiresAt is its source/policy
// freshness deadline. A zero timestamp means no evidence for that timestamp;
// in particular, absent expiry does not assert that metadata stays fresh forever.
// Consumers decide freshness using their own clock; this value starts no timer.
type MetadataProvenance struct {
	Source     MetadataSource
	Snapshot   string
	Revision   string
	ObservedAt time.Time
	ExpiresAt  time.Time
}

// RateCardReference associates a descriptor with an entry in a separately
// versioned rate card. ID, Revision, and Entry are opaque, non-secret keys, not
// URLs. The zero value means no pricing basis is known. Producers supply all
// three when the association is known; no key is inferred from model spelling.
// This reference specifies no rates, currency, billing dimensions, or arithmetic.
type RateCardReference struct {
	ID       string
	Revision string
	Entry    string
}
