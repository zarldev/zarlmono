package modelsdev

import (
	"context"
	"maps"
	"math"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

// Evidence contains intrinsic catalogue assertions, not effective client, gateway,
// protocol, or deployment support. Reasoning is the catalogue's reasoning flag;
// it does not establish a thinking control or reasoning replay capability.
// All fields are values: callers can retain and modify a result independently.
// SchemaVersion and Provenance are populated by the lookup, from the snapshot
// actually served. SchemaVersion describes the cache format, not a content ID.
// Negative limits and negative/nonfinite rates are unknown independently; valid
// assertions in other fields, including explicitly known zero rates, survive.
type Evidence struct {
	// SchemaVersion is the cache format version, not a snapshot or revision ID.
	SchemaVersion int
	Tools         llm.Support
	Reasoning     llm.Support
	Modalities    DirectionalModalities
	Limits        llm.ModelLimits
	Pricing       PriceEvidence
	Provenance    llm.MetadataProvenance
}

// DirectionalModalities separates catalogue input and output assertions.
type DirectionalModalities struct {
	Input  ModalityEvidence
	Output ModalityEvidence
}

// ModalityEvidence retains the known catalogue modalities, including PDF.
// A missing/null direction is unknown. A present list is exhaustive for known
// modalities: unlisted known modalities are unsupported. Unknown future names
// never establish support for a known modality.
type ModalityEvidence struct {
	Text  llm.Support
	Image llm.Support
	Audio llm.Support
	Video llm.Support
	PDF   llm.Support
}

// PriceEvidence retains independent input/output list prices in USD per million
// tokens, the existing models.dev contract. Known zero differs from absent.
// Negative/nonfinite or unknown rates have value zero and Known false after
// HTTP/cache ingestion; a valid known zero keeps Known true. These rates do not
// identify an account's rate card or establish route billing.
type PriceEvidence struct {
	InputCostPerMTok  float64
	OutputCostPerMTok float64
	InputKnown        bool
	OutputKnown       bool
}

// LookupEvidence loads intrinsic evidence for an exact catalogue provider key
// and model ID. It performs no provider alias translation, normalization, or
// basename fallback, and never resolves an opaque requested alias to a model.
// Like Lookup, it can load/refresh the snapshot and serve stale data on failure;
// use Warm to observe load errors and Provenance to assess the snapshot age.
func (s *Source) LookupEvidence(ctx context.Context, providerKey, model string) (Evidence, bool) {
	snap, ok, _ := s.ensureSnapshot(ctx)
	if !ok {
		return Evidence{}, false
	}
	return lookupEvidence(snap, providerKey, model, s.ttl)
}

// LookupEvidenceCached is the process-local counterpart to LookupEvidence.
// It never reads the backing cache or performs network I/O, even when stale.
// It returns no match until Warm or a loading lookup has populated the snapshot.
func (s *Source) LookupEvidenceCached(providerKey, model string) (Evidence, bool) {
	s.mu.RLock()
	snap, ok := s.snapshot, s.hasSnap
	s.mu.RUnlock()
	if !ok {
		return Evidence{}, false
	}
	return lookupEvidence(snap, providerKey, model, s.ttl)
}

func lookupEvidence(snap Snapshot, providerKey, model string, ttl time.Duration) (Evidence, bool) {
	entry, ok := snap.Entries[providerKey][model]
	if !ok {
		return Evidence{}, false
	}
	evidence := entry.Evidence
	evidence.SchemaVersion = snap.Schema
	evidence.Provenance = llm.MetadataProvenance{
		Source:     llm.MetadataSources.METADATACATALOGUE,
		Snapshot:   snapshotKey,
		Revision:   snap.Revision,
		ObservedAt: snap.FetchedAt,
		ExpiresAt:  snap.FetchedAt.Add(ttl),
	}
	return evidence, true
}

func decodeEntry(m modelWire) Entry {
	evidence := Evidence{
		Tools:     boolSupport(m.ToolCall),
		Reasoning: boolSupport(m.Reasoning),
		Modalities: DirectionalModalities{
			Input:  decodeModalities(m.Modalities.Input),
			Output: decodeModalities(m.Modalities.Output),
		},
		Limits: llm.ModelLimits{
			InputTokens:   int64(m.Limit.Input),
			OutputTokens:  int64(m.Limit.Output),
			ContextTokens: int64(m.Limit.Context),
		},
	}
	if m.Cost.Input != nil {
		evidence.Pricing.InputCostPerMTok, evidence.Pricing.InputKnown = *m.Cost.Input, true
	}
	if m.Cost.Output != nil {
		evidence.Pricing.OutputCostPerMTok, evidence.Pricing.OutputKnown = *m.Cost.Output, true
	}
	return projectEntry(Entry{Evidence: evidence})
}

// projectEntry is the single ingestion policy for HTTP and schema-4 storage.
// Evidence alone supplies the lossy legacy fields; never infer missing evidence
// from those fields. Assign only projections, retaining any other entry fields.
func projectEntry(entry Entry) Entry {
	evidence := entry.Evidence
	evidence.Limits.InputTokens = max(0, evidence.Limits.InputTokens)
	evidence.Limits.OutputTokens = max(0, evidence.Limits.OutputTokens)
	evidence.Limits.ContextTokens = max(0, evidence.Limits.ContextTokens)
	evidence.Pricing.InputCostPerMTok, evidence.Pricing.InputKnown = validRate(evidence.Pricing.InputCostPerMTok, evidence.Pricing.InputKnown)
	evidence.Pricing.OutputCostPerMTok, evidence.Pricing.OutputKnown = validRate(evidence.Pricing.OutputCostPerMTok, evidence.Pricing.OutputKnown)
	// Snapshot metadata is not a per-entry assertion. Lookups attach it from
	// the snapshot actually served, regardless of persisted entry metadata.
	evidence.SchemaVersion = 0
	evidence.Provenance = llm.MetadataProvenance{}
	entry.Evidence = evidence
	entry.Information = information(evidence)
	entry.Intrinsic = Intrinsic{
		ContextWindow:    int(evidence.Limits.ContextTokens),
		MaxOutputTokens:  int(evidence.Limits.OutputTokens),
		SupportsTools:    evidence.Tools == llm.Supports.SUPPORTSUPPORTED,
		SupportsThinking: evidence.Reasoning == llm.Supports.SUPPORTSUPPORTED,
		SupportsVision:   evidence.Modalities.Input.Image == llm.Supports.SUPPORTSUPPORTED,
		SupportsVideo:    evidence.Modalities.Input.Video == llm.Supports.SUPPORTSUPPORTED,
	}
	entry.Pricing = Pricing{
		InputCostPerMTok:  evidence.Pricing.InputCostPerMTok,
		OutputCostPerMTok: evidence.Pricing.OutputCostPerMTok,
	}
	return entry
}

func validRate(rate float64, known bool) (float64, bool) {
	if !known || rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0, false
	}
	return rate, true
}

func boolSupport(value *bool) llm.Support {
	if value == nil {
		return llm.Supports.SUPPORTUNKNOWN
	}
	if *value {
		return llm.Supports.SUPPORTSUPPORTED
	}
	return llm.Supports.SUPPORTUNSUPPORTED
}

func decodeModalities(names []string) ModalityEvidence {
	if names == nil {
		return ModalityEvidence{}
	}
	result := ModalityEvidence{
		Text: llm.Supports.SUPPORTUNSUPPORTED, Image: llm.Supports.SUPPORTUNSUPPORTED,
		Audio: llm.Supports.SUPPORTUNSUPPORTED, Video: llm.Supports.SUPPORTUNSUPPORTED,
		PDF: llm.Supports.SUPPORTUNSUPPORTED,
	}
	for _, name := range names {
		parsed, err := ParseModality(name)
		if err != nil {
			continue // Unknown optional wire names do not invalidate the model.
		}
		switch parsed {
		case Modalities.MODALITYTEXT:
			result.Text = llm.Supports.SUPPORTSUPPORTED
		case Modalities.MODALITYIMAGE:
			result.Image = llm.Supports.SUPPORTSUPPORTED
		case Modalities.MODALITYAUDIO:
			result.Audio = llm.Supports.SUPPORTSUPPORTED
		case Modalities.MODALITYVIDEO:
			result.Video = llm.Supports.SUPPORTSUPPORTED
		case Modalities.MODALITYPDF:
			result.PDF = llm.Supports.SUPPORTSUPPORTED
		case Modalities.MODALITYUNKNOWN:
			// Unknown is not an assertion of support.
		}
	}
	return result
}

// cloneSnapshot separates the Source's immutable maps from cache-owned maps.
// Entry and all evidence beneath it are value-only, so two map levels suffice.
func cloneSnapshot(snap Snapshot) Snapshot {
	snap.Entries = maps.Clone(snap.Entries)
	for key, entries := range snap.Entries {
		snap.Entries[key] = maps.Clone(entries)
	}
	return snap
}

// loadSnapshot owns its maps before validating external persisted assertions.
// The schema-4 Evidence is authoritative even when legacy fields disagree.
func loadSnapshot(snap Snapshot) Snapshot {
	snap = cloneSnapshot(snap)
	for _, entries := range snap.Entries {
		for id, entry := range entries {
			entries[id] = projectEntry(entry)
		}
	}
	return snap
}

// information projects the native catalogue vocabulary onto C1's four modalities.
// PDF remains native evidence only, never an effective content modality.
func information(e Evidence) ModelInformation {
	return ModelInformation{
		Tools: e.Tools, Reasoning: e.Reasoning, Limits: e.Limits,
		Modalities: llm.DirectionalModalities{Input: contentModalities(e.Modalities.Input), Output: contentModalities(e.Modalities.Output)},
	}
}

func contentModalities(m ModalityEvidence) llm.ModalitySupport {
	return llm.ModalitySupport{Text: m.Text, Image: m.Image, Audio: m.Audio, Video: m.Video}
}
