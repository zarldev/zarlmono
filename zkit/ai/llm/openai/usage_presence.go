package openai

import (
	"bytes"
	"encoding/json"
)

// reportedJSONUsage distinguishes an absent/null field from a reported zero
// object. The surrounding payload has already crossed the SDK JSON boundary.
func reportedJSONUsage(raw string) bool {
	var envelope struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return false
	}
	return len(envelope.Usage) != 0 && !bytes.Equal(bytes.TrimSpace(envelope.Usage), []byte("null"))
}
