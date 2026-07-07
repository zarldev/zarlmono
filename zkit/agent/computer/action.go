package computer

// Action describes an operation to perform on a computer surface.
type Action struct {
	Kind   ActionKind `json:"kind"`
	Target *TargetRef `json:"target,omitempty"`
	Value  string     `json:"value,omitempty"`
	Key    string     `json:"key,omitempty"`
	URL    string     `json:"url,omitempty"`
	Delta  *Point     `json:"delta,omitempty"`
}
