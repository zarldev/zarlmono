package tools

// OutputFormat selects how a tool result renders for the model: labelled
// plaintext (the default) or JSON. Both views come from the SAME structured
// data the tool already collected; only the rendering — the result's
// String() — differs. The labelled form is what small models read best and
// what the production registry defaults to; a caller opts into JSON per-call
// via the tool's `output` argument. Shared across tool packages so every
// tool's `output` argument means the same thing.
type OutputFormat string

const (
	// OutputLabeled is the default: labelled plaintext (header line + indented
	// rows). It matches the shape ripgrep / IDE search / directory listings
	// use, which is what the model has the strongest training prior on, and is
	// cheaper in tokens than the JSON equivalent.
	OutputLabeled OutputFormat = "labeled"
	// OutputJSON renders the structured data as JSON — the machine-readable
	// shape a caller can parse without re-deriving fields from text.
	OutputJSON OutputFormat = "json"
)

// Resolve returns the format with the empty/zero value defaulted to labelled.
func (f OutputFormat) Resolve() OutputFormat {
	if f == OutputJSON {
		return OutputJSON
	}
	return OutputLabeled
}
