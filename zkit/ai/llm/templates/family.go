package templates

import "strings"

// Family ties a model-ID hint pattern to a concrete ChatTemplate.
// The closed set of supported families is the package-level vars
// below — adding a new family is one var declaration plus inclusion
// in the families list, never an extra `if strings.Contains` branch
// in some scattered call site.
//
// Why a struct registry instead of a string enum?
//
// Different backends label "the same" model differently
// ("qwen3.6-35b-a3b" on llama-server, "qwen3:14b" on Ollama). A
// Family carries its own substring-hint list so the string-matching
// stays colocated with the template and isn't smeared through
// callers as a giant switch.
type Family struct {
	name     string
	template ChatTemplate
	hints    []string // case-insensitive substrings matched against model IDs
}

// Name returns the family's short identifier ("qwen", "gemma").
func (f Family) Name() string { return f.name }

// Template returns the ChatTemplate for this family.
func (f Family) Template() ChatTemplate { return f.template }

// IsZero reports whether f is the unset zero Family{}. Distinguishes
// "InferFamily found nothing" from "explicit Family value".
func (f Family) IsZero() bool { return f.name == "" && f.template == nil }

// Registered families. Add an entry here to introduce a new family
// + template binding. The first entry whose hints match a model ID
// wins, so order from most-specific to least.
var (
	Qwen  = Family{name: "qwen", template: Qwen3{}, hints: []string{"qwen"}}
	Gemma = Family{name: "gemma", template: Gemma4{}, hints: []string{"gemma"}}
)

// families is the canonical lookup list. fallbackFamily is what
// InferFamily returns when no hint matches — Qwen here because it's
// the dominant family in this repo's setup. Change the fallback by
// re-pointing this var, never by mutating the slice.
var (
	families       = []Family{Qwen, Gemma}
	fallbackFamily = Qwen
)

// All returns a copy of the registered families in declaration order.
// Useful for /help text or model-selector UIs.
func All() []Family {
	out := make([]Family, len(families))
	copy(out, families)
	return out
}

// InferFamily picks the Family whose hint list matches modelID, or
// falls back to Qwen if nothing matches. The match is a case-
// insensitive substring scan; first hit wins. Pass an explicit
// Family directly when the inference is wrong for your case.
func InferFamily(modelID string) Family {
	m := strings.ToLower(modelID)
	for _, f := range families {
		for _, h := range f.hints {
			if strings.Contains(m, h) {
				return f
			}
		}
	}
	return fallbackFamily
}

// PickByModel is the one-shot helper: model-ID string in,
// ready-to-use ChatTemplate out. Equivalent to
// InferFamily(modelID).Template() and exists so call sites that
// don't need the Family value can stay terse.
func PickByModel(modelID string) ChatTemplate {
	return InferFamily(modelID).Template()
}
