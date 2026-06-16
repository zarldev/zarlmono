package service

// Coordinates is the browser-supplied user location (lat/lng) threaded
// through to the system prompt's {{location}} placeholder. Zero value
// means "not provided" and renders as UnknownLocation downstream.
type Coordinates struct {
	Lat float64
	Lng float64
}

// Known reports whether the browser supplied a real location. A zero
// Coordinates{} represents "not provided" — treating null-island as
// unknown is fine since no user legitimately reports lat=0,lng=0.
func (c Coordinates) Known() bool {
	return c.Lat != 0 || c.Lng != 0
}
