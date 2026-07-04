package retrieval

// Operator identifies how a metadata field is compared in a retrieval filter.
type Operator string

const (
	// OpEq matches metadata values that are exactly equal to the condition value.
	OpEq Operator = "eq"
)

// Filter narrows retrieval or vector-store searches by document metadata.
type Filter struct {
	Must []Condition `json:"must,omitempty"`
}

// IsZero reports whether f applies no constraints.
func (f Filter) IsZero() bool { return len(f.Must) == 0 }

// Condition is one typed metadata predicate inside a Filter.
type Condition struct {
	Field string   `json:"field"`
	Op    Operator `json:"op"`
	Value any      `json:"value,omitempty"`
}

// Eq returns a metadata equality condition for field.
func Eq(field string, value any) Condition {
	return Condition{Field: field, Op: OpEq, Value: value}
}

// Match returns true when metadata satisfies every condition in f.
func (f Filter) Match(metadata Metadata) bool {
	for _, condition := range f.Must {
		if !condition.Match(metadata) {
			return false
		}
	}
	return true
}

// Match returns true when metadata satisfies c.
func (c Condition) Match(metadata Metadata) bool {
	switch c.Op {
	case OpEq, "":
		return metadata != nil && metadata[c.Field] == c.Value
	default:
		return false
	}
}
