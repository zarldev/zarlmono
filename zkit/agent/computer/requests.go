package computer

// ObserveRequest controls which optional observation fields are populated.
type ObserveRequest struct {
	IncludeScreenshot bool `json:"include_screenshot,omitempty"`
	IncludeTargets    bool `json:"include_targets,omitempty"`
	IncludeText       bool `json:"include_text,omitempty"`
	IncludeRaw        bool `json:"include_raw,omitempty"`
}

// ActionRequest describes an action plus optional activation and completion
// triggers.
type ActionRequest struct {
	Action Action   `json:"action"`
	When   *Trigger `json:"when,omitempty"`
	Until  *Trigger `json:"until,omitempty"`
}
