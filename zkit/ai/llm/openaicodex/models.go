package openaicodex

import (
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const (
	modelGPT55      = "gpt-5.5"
	modelGPT54      = "gpt-5.4"
	modelGPT54Mini  = "gpt-5.4-mini"
	modelGPT53Codex = "gpt-5.3-codex"
	modelGPT53Spark = "gpt-5.3-codex-spark"
	modelGPT52      = "gpt-5.2"
)

// modelPreset describes one selectable model id and the reasoning
// effort it implies. The Codex backend accepts a small set of base
// model names plus a separate `reasoning.effort` field — we surface
// each (model, effort) combination as its own preset so users can
// pick "gpt-5.1-codex-high" directly instead of having to dial in
// options. presetReasoningEffort is empty for base entries that don't
// pin a level.
type modelPreset struct {
	ID              string
	BaseModel       string
	ReasoningEffort reasoningEffort
	Description     string
	// ContextWindow is the usable max-input-tokens cap for this
	// ChatGPT-account Codex backend. 0 means "unknown" — callers fall
	// back to the codex family default (see DefaultContextWindow).
	ContextWindow int
}

// presetModels enumerates the base models the picker surfaces.
// Ordered newest-first so the recommended models sit at the top.
// Canonical catalogue is https://developers.openai.com/codex/models.
//
// Reasoning effort lives on a SEPARATE axis (ReasoningEffort
// setting), not embedded in the model id, so the picker stays short
// (one row per base model instead of one row per model×effort pair)
// and so the user can change effort without re-picking a model.
//
// Power users can still type explicit "<base>-<effort>" ids in
// /model and resolveModel will route them — see suffixedPresets()
// which generates the variants on demand for resolveModel's lookup
// table at init time.
//
// Legacy 5.1-codex / 5.1-codex-max / 5.1-codex-mini / 5.2-codex are
// API-key-only on the OpenAI side; the Codex backend rejects them
// when authed via a ChatGPT account ("model is not supported when
// using Codex with a ChatGPT account"). zarlcode only supports
// OAuth for the codex backend, so listing them in the picker means
// every selection fails. They're excluded.
var presetModels = []modelPreset{
	// gpt-5.5 (newest frontier — supersedes 5.4 for complex work)
	{ID: modelGPT55, BaseModel: modelGPT55, Description: "GPT-5.5 (newest frontier)", ContextWindow: DefaultContextWindow},
	// gpt-5.4 (flagship for professional work)
	{ID: modelGPT54, BaseModel: modelGPT54, Description: "GPT-5.4 (flagship)", ContextWindow: DefaultContextWindow},
	// gpt-5.4-mini (fast, efficient — sub-agent workhorse)
	{
		ID:            modelGPT54Mini,
		BaseModel:     modelGPT54Mini,
		Description:   "GPT-5.4 Mini (fast subagent)",
		ContextWindow: DefaultContextWindow,
	},
	// gpt-5.3-codex (industry-leading coding)
	{
		ID:            modelGPT53Codex,
		BaseModel:     modelGPT53Codex,
		Description:   "GPT-5.3 Codex (coding flagship)",
		ContextWindow: DefaultContextWindow,
	},
	// gpt-5.3-codex-spark (text-only, ChatGPT Pro only — real-time iteration)
	{
		ID:            modelGPT53Spark,
		BaseModel:     modelGPT53Spark,
		Description:   "GPT-5.3 Codex Spark (real-time, Pro only)",
		ContextWindow: DefaultContextWindow,
	},
	// gpt-5.2 (previous-gen general-purpose)
	{ID: modelGPT52, BaseModel: modelGPT52, Description: "GPT-5.2 (previous gen)", ContextWindow: DefaultContextWindow},
}

// DefaultContextWindow is the conservative compaction budget for the
// ChatGPT-account Codex backend. zarlcode prefers live /codex/models metadata
// when the OAuth vault is available; this fallback covers startup without a
// token, probe failures, and consumers outside the TUI. Over-advertising here
// delays pressure-gated compaction until after the backend hard-fails with
// "input exceeds the context window".
const DefaultContextWindow = 256_000

// ContextWindowFor returns the usable context window for a codex
// model id. Handles both base ids (modelGPT55) and synthetic effort
// suffixes ("gpt-5.5-high") by routing through resolveModel.
func ContextWindowFor(id string) int {
	if p, ok := presetByID[id]; ok && p.ContextWindow > 0 {
		return p.ContextWindow
	}
	// Fall back to the base model's window when only an effort-suffixed
	// id was provided but the base entry holds the cap.
	base, _ := resolveModel(id)
	if base != id {
		if p, ok := presetByID[base]; ok && p.ContextWindow > 0 {
			return p.ContextWindow
		}
	}
	return DefaultContextWindow
}

// effortVariants maps a base model to the reasoning efforts it
// supports. Used by suffixedPresets() to populate presetByID with
// resolvable "<base>-<effort>" ids for power-user direct entry, and
// by the settings pane (later) to constrain the effort dropdown to
// what the active model actually accepts.
var effortVariants = map[string][]reasoningEffort{
	modelGPT55: {
		reasoningEffortNone,
		reasoningEffortLow,
		reasoningEffortMedium,
		reasoningEffortHigh,
		reasoningEffortXHigh,
	},
	modelGPT54: {
		reasoningEffortNone,
		reasoningEffortLow,
		reasoningEffortMedium,
		reasoningEffortHigh,
		reasoningEffortXHigh,
	},
	modelGPT54Mini:  {reasoningEffortLow, reasoningEffortMedium, reasoningEffortHigh},
	modelGPT53Codex: {reasoningEffortLow, reasoningEffortMedium, reasoningEffortHigh, reasoningEffortXHigh},
	modelGPT53Spark: {reasoningEffortLow},
	modelGPT52: {
		reasoningEffortNone,
		reasoningEffortLow,
		reasoningEffortMedium,
		reasoningEffortHigh,
		reasoningEffortXHigh,
	},
}

// EffortVariants returns the supported reasoning efforts for the
// given base model id, or nil for unknown models. Exported so the
// settings pane can build a constrained dropdown.
func EffortVariants(baseModel string) []string {
	efforts, ok := effortVariants[baseModel]
	if !ok {
		return nil
	}
	out := make([]string, len(efforts))
	for i, e := range efforts {
		out[i] = string(e)
	}
	return out
}

// suffixedPresets returns the synthetic "<base>-<effort>" entries
// for every effort each base model supports. Added to presetByID at
// init() so direct id entry (e.g. /model gpt-5.4-high) still works
// even though those ids no longer appear in the picker. Picker
// rendering uses presetModels (base-only); resolution uses the
// merged map.
func suffixedPresets() []modelPreset {
	var out []modelPreset
	for _, base := range presetModels {
		for _, eff := range effortVariants[base.BaseModel] {
			out = append(out, modelPreset{
				ID:              base.BaseModel + "-" + string(eff),
				BaseModel:       base.BaseModel,
				ReasoningEffort: eff,
				Description:     base.Description + " (" + string(eff) + " reasoning)",
				ContextWindow:   base.ContextWindow,
			})
		}
	}
	return out
}

// presetByID indexes presetModels by ID for O(1) lookup at request
// time. Populated in init().
var presetByID = map[string]modelPreset{}

func init() {
	// Base models go in first (these are what the picker shows).
	for _, p := range presetModels {
		presetByID[p.ID] = p
	}
	// Then synthetic "<base>-<effort>" entries so direct typed entry
	// (e.g. /model gpt-5.4-high) still resolves to the right base +
	// effort even though they're not in the picker list.
	for _, p := range suffixedPresets() {
		presetByID[p.ID] = p
	}
}

// resolveModel maps a user-facing model id (which may be a "preset"
// like "gpt-5.1-codex-high") to the base model + reasoning effort the
// Codex backend actually accepts. Unknown ids pass through as-is so
// callers can still send experimental model names.
func resolveModel(id string) (string, reasoningEffort) {
	if p, ok := presetByID[id]; ok {
		return p.BaseModel, p.ReasoningEffort
	}
	return id, ""
}

// ListPresetModels returns the full preset catalogue in registration
// order. Used by the model registry to expose Codex models in the
// settings picker.
func ListPresetModels() []llm.Model {
	out := make([]llm.Model, 0, len(presetModels))
	for _, p := range presetModels {
		ctx := p.ContextWindow
		if ctx == 0 {
			ctx = DefaultContextWindow
		}
		out = append(out, llm.Model{
			ID:          p.ID,
			Name:        p.ID,
			Description: p.Description,
			MaxTokens:   ctx,
			InputCost:   0, // covered by ChatGPT subscription
			OutputCost:  0,
			Capabilities: llm.ModelCapabilities{
				SupportsStreaming: true,
				SupportsTools:     true,
				SupportsSystem:    true,
				SupportsThinking:  true,
				SupportsVision:    true,
				SupportsVideo:     true,
			},
		})
	}
	return out
}
