package computer

// Trigger describes a condition used to gate or settle an action.
type Trigger struct {
	Kind   TriggerKind `json:"kind"`
	Target *TargetRef  `json:"target,omitempty"`
	Text   string      `json:"text,omitempty"`
	Value  string      `json:"value,omitempty"`
	URL    string      `json:"url,omitempty"`
}
