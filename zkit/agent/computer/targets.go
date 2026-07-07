package computer

// TargetRef identifies a target for an action or trigger using semantic,
// locator, identifier, or positional hints.
type TargetRef struct {
	ID       string `json:"id,omitempty"`
	Role     string `json:"role,omitempty"`
	Name     string `json:"name,omitempty"`
	Text     string `json:"text,omitempty"`
	Locator  string `json:"locator,omitempty"`
	Position *Point `json:"position,omitempty"`
}

// TargetDescriptor describes a surface target discovered during observation.
type TargetDescriptor struct {
	ID          string `json:"id"`
	Role        string `json:"role,omitempty"`
	Name        string `json:"name,omitempty"`
	Text        string `json:"text,omitempty"`
	Description string `json:"description,omitempty"`
	Bounds      *Rect  `json:"bounds,omitempty"`
	Enabled     bool   `json:"enabled,omitempty"`
	Visible     bool   `json:"visible,omitempty"`
	Focused     bool   `json:"focused,omitempty"`
	Value       string `json:"value,omitempty"`
}
