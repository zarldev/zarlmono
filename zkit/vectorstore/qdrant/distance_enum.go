package qdrant

//go:generate go tool goenums -f distance_enum.go

// distance is the goenums source for Distance, the closed set of vector
// distance functions accepted by Qdrant. Trailing comments are the exact wire
// values required by the Qdrant REST API.
type distance int

const (
	cosine    distance = iota // Cosine
	dot                       // Dot
	euclid                    // Euclid
	manhattan                 // Manhattan
)
