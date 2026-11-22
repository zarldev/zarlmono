package backends

import (
	"cmp"
	"slices"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// ModelEvidence asserts one layer's protocol surfaces, coupled to their source.
// Assertions are borrowed only for the duration of ResolveModelCached.
type ModelEvidence struct {
	Assertions map[llm.ModelProtocol]llm.ModelSurface
	Provenance llm.MetadataProvenance
}

// ModelCandidate describes an explicitly identified possible deployment. The
// catalogue organization is independent of the serving provider and rate card.
type ModelCandidate struct {
	CanonicalModel        llm.ModelID
	ServingProvider       llm.ServingProviderID
	Deployment            llm.DeploymentID
	CatalogueProvider     string
	Model                 []ModelEvidence
	DeploymentConstraints []ModelEvidence
	Gateway               []ModelEvidence
	RateCard              llm.RateCardReference
}

// ModelBinding supplies a route's possible deployments and shared constraints.
// Direct omits the gateway layer; Exhaustive asserts there are no other candidates.
// All identities are opaque, and all collections are borrowed only during resolution.
type ModelBinding struct {
	Route          llm.RouteID
	RequestedModel llm.ModelID
	Direct         bool
	Exhaustive     bool
	Candidates     []ModelCandidate
	Client         []ModelEvidence
	Policy         []ModelEvidence
}

// ModelResolutionIssue records a conflict, age, or completeness diagnostic.
// Candidate is -1 for binding-wide issues. Field is a diagnostic path, not a
// machine-readable vocabulary. Evidence is an independently owned collection.
type ModelResolutionIssue struct {
	Kind      ModelResolutionIssueKind
	Layer     ModelEvidenceLayer
	Candidate int
	Protocol  llm.ModelProtocol
	Field     string
	Evidence  []llm.MetadataProvenance
}

// ModelResolution is an independently owned, conservative cached snapshot.
type ModelResolution struct {
	Descriptor llm.ModelDescriptor
	Issues     []ModelResolutionIssue
}

// ResolveModelCached intersects explicit evidence across required layers and all
// possible deployments. It performs no I/O, alias inference, or legacy lookup.
// Catalogue evidence enriches only tools, directional modalities, and ceilings
// on protocols already represented by supplied assertions. It establishes no
// protocol or reasoning support. The caller supplies the freshness clock.
//
// Positive ceilings are upper-bound restrictions, independent of support and
// freshness. Any non-policy layer can bound an otherwise unknown dimension;
// policy only lowers an already known bound. Across candidates, a missing bound
// or non-exhaustive candidate set makes that dimension unknown.
func (r *ProviderRegistry) ResolveModelCached(binding ModelBinding, at time.Time) ModelResolution {
	work := catalogResolution{
		at:        at,
		protocols: bindingProtocols(binding),
		result: ModelResolution{Descriptor: llm.ModelDescriptor{
			Route: binding.Route, RequestedModel: binding.RequestedModel,
			Surfaces: make(map[llm.ModelProtocol]llm.ModelSurface),
		}},
	}
	client := work.consolidate(binding.Client, ModelEvidenceLayers.LAYERCLIENT, -1, false)
	policy := work.consolidate(binding.Policy, ModelEvidenceLayers.LAYERPOLICY, -1, true)
	for index, candidate := range binding.Candidates {
		model := slices.Clone(candidate.Model)
		if r.modelsDevSource != nil && candidate.CanonicalModel != "" && candidate.CatalogueProvider != "" {
			if record, ok := r.modelsDevSource.LookupModelCached(candidate.CatalogueProvider, candidate.CanonicalModel); ok {
				assertions := make(map[llm.ModelProtocol]llm.ModelSurface, len(work.protocols))
				for _, protocol := range work.protocols {
					assertions[protocol] = llm.ModelSurface{
						Capabilities: llm.EffectiveCapabilities{Tools: record.Information.Tools},
						Modalities:   record.Information.Modalities,
						Limits:       record.Information.Limits,
					}
				}
				model = append(model, ModelEvidence{Assertions: assertions, Provenance: record.Provenance})
			}
		}
		layers := []map[llm.ModelProtocol]llm.ModelSurface{
			work.consolidate(model, ModelEvidenceLayers.LAYERMODEL, index, false),
			work.consolidate(candidate.DeploymentConstraints, ModelEvidenceLayers.LAYERDEPLOYMENT, index, false),
			client,
		}
		if !binding.Direct {
			layers = append(layers, work.consolidate(candidate.Gateway, ModelEvidenceLayers.LAYERGATEWAY, index, false))
		}
		for _, protocol := range work.protocols {
			surface := candidateSurface(layers, policy[protocol], protocol)
			if index != 0 {
				surface = intersectCandidates(work.result.Descriptor.Surfaces[protocol], surface)
			}
			work.result.Descriptor.Surfaces[protocol] = surface
		}
	}
	work.finish(binding)
	return work.result
}

// LegacyCapabilities projects explicit Supported features only when protocol
// Support is also explicitly Supported. Vision and video describe input
// processing only; output modalities never imply input support. Unknown and
// Unsupported are false, with no streaming or system defaults.
func LegacyCapabilities(surface llm.ModelSurface) llm.ModelCapabilities {
	if surface.Support != llm.Supports.SUPPORTSUPPORTED {
		return llm.ModelCapabilities{}
	}
	return llm.ModelCapabilities{
		SupportsStreaming: surface.Capabilities.Streaming == llm.Supports.SUPPORTSUPPORTED,
		SupportsVision:    surface.Modalities.Input.Image == llm.Supports.SUPPORTSUPPORTED,
		SupportsVideo:     surface.Modalities.Input.Video == llm.Supports.SUPPORTSUPPORTED,
		SupportsTools:     surface.Capabilities.Tools == llm.Supports.SUPPORTSUPPORTED,
		SupportsSystem:    surface.Capabilities.SystemMessages == llm.Supports.SUPPORTSUPPORTED,
		SupportsThinking:  surface.Capabilities.Thinking == llm.Supports.SUPPORTSUPPORTED,
	}
}

// catalogResolution owns only the current call's snapshot and diagnostics.
type catalogResolution struct {
	at        time.Time
	protocols []llm.ModelProtocol
	result    ModelResolution
}

func bindingProtocols(binding ModelBinding) []llm.ModelProtocol {
	protocols := make(map[llm.ModelProtocol]struct{})
	add := func(evidence []ModelEvidence) {
		for _, item := range evidence {
			for protocol := range item.Assertions {
				protocols[protocol] = struct{}{}
			}
		}
	}
	add(binding.Client)
	add(binding.Policy)
	for _, candidate := range binding.Candidates {
		add(candidate.Model)
		add(candidate.DeploymentConstraints)
		add(candidate.Gateway)
	}
	ordered := make([]llm.ModelProtocol, 0, len(protocols))
	for protocol := range protocols {
		ordered = append(ordered, protocol)
	}
	slices.SortFunc(ordered, func(a, b llm.ModelProtocol) int { return cmp.Compare(a.String(), b.String()) })
	return ordered
}

func (w *catalogResolution) issue(kind ModelResolutionIssueKind, layer ModelEvidenceLayer, candidate int, protocol llm.ModelProtocol, field string, evidence []llm.MetadataProvenance) {
	w.result.Issues = append(w.result.Issues, ModelResolutionIssue{
		Kind: kind, Layer: layer, Candidate: candidate, Protocol: protocol,
		Field: field, Evidence: slices.Clone(evidence),
	})
}

// positiveFresh reports whether this snapshot may grant support. Restrictions
// remain visible regardless of age; timestamps never trigger a refresh.
func (w *catalogResolution) positiveFresh(provenance llm.MetadataProvenance, layer ModelEvidenceLayer, candidate int) bool {
	configuration := provenance.Source == llm.MetadataSources.METADATACONFIGURATION
	undated := provenance.ObservedAt.After(w.at) || (!configuration && (provenance.ObservedAt.IsZero() || provenance.ExpiresAt.IsZero()))
	stale := !provenance.ExpiresAt.IsZero() && !w.at.Before(provenance.ExpiresAt)
	unknownSource := provenance.Source == llm.MetadataSources.METADATAUNKNOWN
	if undated || unknownSource {
		w.issue(ModelResolutionIssueKinds.RESOLUTIONUNDATED, layer, candidate, llm.ModelProtocol{}, "provenance", []llm.MetadataProvenance{provenance})
	}
	if stale {
		w.issue(ModelResolutionIssueKinds.RESOLUTIONSTALE, layer, candidate, llm.ModelProtocol{}, "provenance", []llm.MetadataProvenance{provenance})
	}
	return !undated && !stale && !unknownSource
}

func (w *catalogResolution) consolidate(evidence []ModelEvidence, layer ModelEvidenceLayer, candidate int, policy bool) map[llm.ModelProtocol]llm.ModelSurface {
	fresh := make([]bool, len(evidence))
	for index, item := range evidence {
		w.result.Descriptor.Evidence = append(w.result.Descriptor.Evidence, item.Provenance)
		fresh[index] = w.positiveFresh(item.Provenance, layer, candidate)
	}
	result := make(map[llm.ModelProtocol]llm.ModelSurface, len(w.protocols))
	for _, protocol := range w.protocols {
		var claims [18]llm.Support
		var limits llm.ModelLimits
		for field := range claims {
			claims[field] = w.consolidateClaim(evidence, fresh, layer, candidate, protocol, field, policy)
		}
		for _, item := range evidence {
			limits = lowerLimits(limits, item.Assertions[protocol].Limits)
		}
		result[protocol] = surfaceFromClaims(claims, limits)
	}
	return result
}

func (w *catalogResolution) consolidateClaim(evidence []ModelEvidence, fresh []bool, layer ModelEvidenceLayer, candidate int, protocol llm.ModelProtocol, field int, policy bool) llm.Support {
	var positive, negative, authorized bool
	var provenance []llm.MetadataProvenance
	for index, item := range evidence {
		claim := surfaceClaims(item.Assertions[protocol])[field]
		if claim == llm.Supports.SUPPORTUNKNOWN {
			continue
		}
		provenance = append(provenance, item.Provenance)
		positive = positive || claim == llm.Supports.SUPPORTSUPPORTED
		negative = negative || claim == llm.Supports.SUPPORTUNSUPPORTED
		authorized = authorized || (claim == llm.Supports.SUPPORTSUPPORTED && fresh[index])
	}
	if policy { // A policy grant is neutral and cannot erase a restriction.
		if negative {
			return llm.Supports.SUPPORTUNSUPPORTED
		}
		return llm.Supports.SUPPORTUNKNOWN
	}
	if positive && negative {
		w.issue(ModelResolutionIssueKinds.RESOLUTIONCONFLICT, layer, candidate, protocol, claimFields()[field], provenance)
		return llm.Supports.SUPPORTUNKNOWN
	}
	if negative {
		return llm.Supports.SUPPORTUNSUPPORTED
	}
	if authorized {
		return llm.Supports.SUPPORTSUPPORTED
	}
	if field == 0 && !positive {
		w.issue(ModelResolutionIssueKinds.RESOLUTIONINCOMPLETE, layer, candidate, protocol, "support", nil)
	}
	return llm.Supports.SUPPORTUNKNOWN
}

func (w *catalogResolution) finish(binding ModelBinding) {
	if len(binding.Candidates) == 0 || !binding.Exhaustive {
		w.issue(ModelResolutionIssueKinds.RESOLUTIONINCOMPLETE, ModelEvidenceLayer{}, -1, llm.ModelProtocol{}, "candidates", nil)
		for _, protocol := range w.protocols {
			w.result.Descriptor.Surfaces[protocol] = intersectCandidates(w.result.Descriptor.Surfaces[protocol], llm.ModelSurface{})
		}
	}
	if binding.Exhaustive && len(binding.Candidates) > 0 {
		w.identities(binding.Candidates)
	}
	if len(w.protocols) == 0 && len(binding.Candidates) > 0 {
		w.issue(ModelResolutionIssueKinds.RESOLUTIONINCOMPLETE, ModelEvidenceLayer{}, -1, llm.ModelProtocol{}, "protocols", nil)
	}
}

func (w *catalogResolution) identities(candidates []ModelCandidate) {
	first := candidates[0]
	w.result.Descriptor.CanonicalModel = first.CanonicalModel
	w.result.Descriptor.ServingProvider = first.ServingProvider
	w.result.Descriptor.Deployment = first.Deployment
	w.result.Descriptor.RateCard = first.RateCard
	for _, candidate := range candidates[1:] {
		if candidate.CanonicalModel != first.CanonicalModel {
			w.result.Descriptor.CanonicalModel = ""
		}
		if candidate.ServingProvider != first.ServingProvider {
			w.result.Descriptor.ServingProvider = ""
		}
		if candidate.Deployment != first.Deployment {
			w.result.Descriptor.Deployment = ""
		}
		if candidate.RateCard != first.RateCard {
			w.result.Descriptor.RateCard = llm.RateCardReference{}
		}
	}
}

func candidateSurface(layers []map[llm.ModelProtocol]llm.ModelSurface, policy llm.ModelSurface, protocol llm.ModelProtocol) llm.ModelSurface {
	claims := surfaceClaims(layers[0][protocol])
	limits := layers[0][protocol].Limits
	for _, layer := range layers[1:] {
		other := layer[protocol]
		otherClaims := surfaceClaims(other)
		for field := range claims {
			claims[field] = intersectSupport(claims[field], otherClaims[field])
		}
		limits = lowerLimits(limits, other.Limits)
	}
	policyClaims := surfaceClaims(policy)
	for field := range claims {
		if policyClaims[field] == llm.Supports.SUPPORTUNSUPPORTED {
			claims[field] = llm.Supports.SUPPORTUNSUPPORTED
		}
	}
	// Features require an effective inference path; ceilings remain independent.
	for field := 1; field < len(claims); field++ {
		claims[field] = intersectSupport(claims[field], claims[0])
	}
	return surfaceFromClaims(claims, llm.ModelLimits{
		InputTokens:   lowerKnown(limits.InputTokens, policy.Limits.InputTokens),
		OutputTokens:  lowerKnown(limits.OutputTokens, policy.Limits.OutputTokens),
		ContextTokens: lowerKnown(limits.ContextTokens, policy.Limits.ContextTokens),
	})
}

func intersectCandidates(a, b llm.ModelSurface) llm.ModelSurface {
	claims, other := surfaceClaims(a), surfaceClaims(b)
	for field := range claims {
		claims[field] = intersectSupport(claims[field], other[field])
	}
	return surfaceFromClaims(claims, llm.ModelLimits{
		InputTokens:   commonCeiling(a.Limits.InputTokens, b.Limits.InputTokens),
		OutputTokens:  commonCeiling(a.Limits.OutputTokens, b.Limits.OutputTokens),
		ContextTokens: commonCeiling(a.Limits.ContextTokens, b.Limits.ContextTokens),
	})
}

func intersectSupport(a, b llm.Support) llm.Support {
	if a == llm.Supports.SUPPORTUNSUPPORTED || b == llm.Supports.SUPPORTUNSUPPORTED {
		return llm.Supports.SUPPORTUNSUPPORTED
	}
	if a == llm.Supports.SUPPORTSUPPORTED && b == llm.Supports.SUPPORTSUPPORTED {
		return llm.Supports.SUPPORTSUPPORTED
	}
	return llm.Supports.SUPPORTUNKNOWN
}

func lowerCeiling(a, b int64) int64 {
	if a == 0 {
		return b
	}
	return lowerKnown(a, b)
}

func lowerKnown(a, b int64) int64 {
	if b == 0 {
		return a
	}
	return min(a, b)
}

func commonCeiling(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	return min(a, b)
}

func lowerLimits(a, b llm.ModelLimits) llm.ModelLimits {
	return llm.ModelLimits{
		InputTokens:   lowerCeiling(a.InputTokens, b.InputTokens),
		OutputTokens:  lowerCeiling(a.OutputTokens, b.OutputTokens),
		ContextTokens: lowerCeiling(a.ContextTokens, b.ContextTokens),
	}
}

// These parallel fixed shapes keep every feature's consolidation and intersection
// identical without reflecting over public domain structs or inventing defaults.
func claimFields() [18]string {
	return [18]string{
		"support", "streaming", "tools", "system_messages", "json_object", "json_schema",
		"thinking", "reasoning_output", "reasoning_replay", "prompt_caching",
		"input.text", "input.image", "input.audio", "input.video",
		"output.text", "output.image", "output.audio", "output.video",
	}
}

func surfaceClaims(s llm.ModelSurface) [18]llm.Support {
	return [18]llm.Support{
		s.Support, s.Capabilities.Streaming, s.Capabilities.Tools,
		s.Capabilities.SystemMessages, s.Capabilities.JSONObject, s.Capabilities.JSONSchema,
		s.Capabilities.Thinking, s.Capabilities.ReasoningOutput, s.Capabilities.ReasoningReplay,
		s.Capabilities.PromptCaching,
		s.Modalities.Input.Text, s.Modalities.Input.Image, s.Modalities.Input.Audio, s.Modalities.Input.Video,
		s.Modalities.Output.Text, s.Modalities.Output.Image, s.Modalities.Output.Audio, s.Modalities.Output.Video,
	}
}

func surfaceFromClaims(c [18]llm.Support, limits llm.ModelLimits) llm.ModelSurface {
	return llm.ModelSurface{
		Support: c[0],
		Capabilities: llm.EffectiveCapabilities{
			Streaming: c[1], Tools: c[2], SystemMessages: c[3], JSONObject: c[4], JSONSchema: c[5],
			Thinking: c[6], ReasoningOutput: c[7], ReasoningReplay: c[8], PromptCaching: c[9],
		},
		Modalities: llm.DirectionalModalities{
			Input:  llm.ModalitySupport{Text: c[10], Image: c[11], Audio: c[12], Video: c[13]},
			Output: llm.ModalitySupport{Text: c[14], Image: c[15], Audio: c[16], Video: c[17]},
		},
		Limits: limits,
	}
}
