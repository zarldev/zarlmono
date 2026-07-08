package computer

// Point describes a two-dimensional point or delta in surface coordinates.
type Point struct {
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`
}

// Rect describes a surface-aligned rectangle.
type Rect struct {
	X      int `json:"x,omitempty"`
	Y      int `json:"y,omitempty"`
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}
