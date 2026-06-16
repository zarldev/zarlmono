package service

import (
	"fmt"
	"strings"
)

// DefaultAgentName is used when no operator-configured name exists yet.
const DefaultAgentName = "Zarl"

// UnknownPersonName is the fallback for {{person_name}} when face
// recognition hasn't identified the current speaker. Phrased so it reads
// naturally in every position the placeholder can appear.
const UnknownPersonName = "the current user"

// UnknownLocation is the fallback for {{location}} when the browser hasn't
// supplied coordinates for the session.
const UnknownLocation = "unknown"

// FormatLocation renders coordinates for the {{location}} placeholder. The
// zero value (the browser's "not provided" sentinel) resolves to
// UnknownLocation so the LLM doesn't believe it's at null-island.
func FormatLocation(c Coordinates) string {
	if !c.Known() {
		return UnknownLocation
	}
	return fmt.Sprintf("latitude %.4f, longitude %.4f", c.Lat, c.Lng)
}

// RenderSystemPrompt substitutes prompt placeholders with live values:
//
//   - {{agent_name}}  → the spoken name the assistant uses for itself
//   - {{person_name}} → the currently identified user (per-session, per-turn)
//   - {{location}}    → the user's coordinates, or "unknown" if absent
//
// Empty values fall back to the defaults so a rendered prompt never leaks
// the raw placeholder to the model.
func RenderSystemPrompt(template, agentName, personName, location string) string {
	if agentName == "" {
		agentName = DefaultAgentName
	}
	if personName == "" {
		personName = UnknownPersonName
	}
	if location == "" {
		location = UnknownLocation
	}
	template = strings.ReplaceAll(template, "{{agent_name}}", agentName)
	template = strings.ReplaceAll(template, "{{person_name}}", personName)
	template = strings.ReplaceAll(template, "{{location}}", location)
	return template
}
