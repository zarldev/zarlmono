package computer

// Observation captures the currently observed state of a computer surface.
type Observation struct {
	Surface       SurfaceInfo        `json:"surface"`
	FocusedTarget *TargetDescriptor  `json:"focused_target,omitempty"`
	Targets       []TargetDescriptor `json:"targets,omitempty"`
	VisibleText   string             `json:"visible_text,omitempty"`
	Screenshot    *ObservationImage  `json:"screenshot,omitempty"`
	Hints         []string           `json:"hints,omitempty"`
	Raw           map[string]any     `json:"raw,omitempty"`
}

// SurfaceInfo describes the observed surface itself.
type SurfaceInfo struct {
	Kind   SurfaceKind `json:"kind"`
	Title  string      `json:"title,omitempty"`
	URL    string      `json:"url,omitempty"`
	Width  int         `json:"width,omitempty"`
	Height int         `json:"height,omitempty"`
}
