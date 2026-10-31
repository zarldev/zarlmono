package search

import (
	"encoding/json"
	"errors"
	"strings"
)

// ErrUnavailable identifies a search with no hits and reported source failures.
// It does not imply every upstream engine failed or that the query has no matches.
var ErrUnavailable = errors.New("search unavailable: no results returned; some sources reported failures")

// SourceFailure describes one upstream failure without exposing its raw message.
// Values returned by the SearXNG backend have bounded source labels and one of
// these reasons: challenge, rate_limited, timeout, http_error, unavailable, unknown.
type SourceFailure struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

const (
	maxSourceFailures = 8
	unknownDiagnostic = "unknown"
)

// sourceFailures parses metadata independently of hits so malformed diagnostics
// cannot discard useful results. Only normalized labels and reasons cross this boundary.
func sourceFailures(metadata json.RawMessage) []SourceFailure {
	if len(metadata) == 0 || string(metadata) == "null" {
		return nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(metadata, &entries); err != nil {
		return []SourceFailure{{Source: unknownDiagnostic, Reason: unknownDiagnostic}}
	}
	failures := make([]SourceFailure, 0, min(len(entries), maxSourceFailures))
	seen := make(map[SourceFailure]bool)
	for _, entry := range entries {
		failure := SourceFailure{Source: unknownDiagnostic, Reason: unknownDiagnostic}
		var tuple []json.RawMessage
		if err := json.Unmarshal(entry, &tuple); err == nil {
			var source, message string
			if len(tuple) > 0 && json.Unmarshal(tuple[0], &source) == nil {
				failure.Source = sourceLabel(source)
			}
			if len(tuple) > 1 && json.Unmarshal(tuple[1], &message) == nil {
				failure.Reason = failureReason(message)
			}
		}
		if seen[failure] {
			continue
		}
		seen[failure] = true
		failures = append(failures, failure)
		if len(failures) == maxSourceFailures {
			break
		}
	}
	return failures
}

// Engine identifiers are labels, not URLs or free-form diagnostic text. Reject
// controls, markup and non-identifier characters rather than repairing them.
func sourceLabel(source string) string {
	if source == "" {
		return unknownDiagnostic
	}
	for _, r := range source {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(" _-.", r) {
			continue
		}
		return unknownDiagnostic
	}
	source = strings.Join(strings.Fields(source), " ")
	if source == "" {
		return unknownDiagnostic
	}
	// Allowed labels are ASCII, so this bound cannot split UTF-8.
	return source[:min(len(source), 64)]
}

func failureReason(message string) string {
	message = strings.ToLower(message)
	switch {
	case strings.Contains(message, "captcha"), strings.Contains(message, "challenge"):
		return "challenge"
	case strings.Contains(message, "too many requests"), strings.Contains(message, "toomanyrequests"), strings.Contains(message, "rate limit"), strings.Contains(message, "429"):
		return "rate_limited"
	case strings.Contains(message, "timeout"), strings.Contains(message, "timed out"):
		return "timeout"
	case strings.Contains(message, "http"):
		return "http_error"
	case strings.Contains(message, "unavailable"), strings.Contains(message, "suspended"):
		return "unavailable"
	default:
		return unknownDiagnostic
	}
}

func failureSummary(failures []SourceFailure) string {
	var b strings.Builder
	for i, failure := range failures {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(failure.Source)
		b.WriteString(": ")
		b.WriteString(failure.Reason)
	}
	return b.String()
}
