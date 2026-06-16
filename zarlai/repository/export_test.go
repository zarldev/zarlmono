package repository

import "encoding/json"

// ParseEmbeddings exposes parseEmbeddings for black-box tests.
func ParseEmbeddings(raw json.RawMessage) ([][]float32, error) {
	return parseEmbeddings(raw)
}
