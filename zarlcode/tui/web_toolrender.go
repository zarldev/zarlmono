package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/ai/tools/search"
)

// webResultLines strips terminal controls from external fields before applying
// our own styles, including bidi controls that could visually spoof a URL or
// target. Hard wrapping also keeps unbroken URLs inside the viewport.
func webResultLines(width int, text string, style func(string) string) []string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, ansi.Strip(text))
	lines := strings.Split(ansi.Hardwrap(strings.ReplaceAll(text, "\t", "    "), max(1, width), true), "\n")
	for i := range lines {
		lines[i] = style(lines[i])
	}
	return lines
}

func renderWebSearchResult(width int, r search.Result) []string {
	out := webResultLines(width, fmt.Sprintf("%d results · %s", len(r.Hits), r.Query), palette.Muted.On)
	if len(r.Hits) == 0 {
		out = append(out, webResultLines(width, "(no results)", palette.Muted.On)...)
	}
	for i, hit := range r.Hits {
		out = append(out, "")
		out = append(out, webResultLines(width, fmt.Sprintf("%d. %s", i+1, hit.Title), palette.Fg.On)...)
		out = append(out, webResultLines(width, hit.URL, palette.Secondary.On)...)
		if hit.Content != "" {
			out = append(out, webResultLines(width, hit.Content, palette.Muted.On)...)
		}
	}
	if len(r.Suggestions) > 0 {
		out = append(out, webResultLines(width, "Suggestions: "+strings.Join(r.Suggestions, ", "), palette.Muted.On)...)
	}
	return out
}

func renderComputerObservation(width int, r computer.Observation) []string {
	out := webResultLines(width, strings.TrimSpace(r.Surface.Kind.String()+" · "+r.Surface.Title), palette.Fg.On)
	if r.Surface.URL != "" {
		out = append(out, webResultLines(width, r.Surface.URL, palette.Secondary.On)...)
	}
	if r.Surface.Width > 0 && r.Surface.Height > 0 {
		out = append(out, webResultLines(width, fmt.Sprintf("Surface: %d × %d", r.Surface.Width, r.Surface.Height), palette.Muted.On)...)
	}
	if r.Screenshot != nil {
		out = append(out, webResultLines(width, "Screenshot attached · "+r.Screenshot.MIMEType, palette.Muted.On)...)
	}
	if r.FocusedTarget != nil {
		out = append(out, webResultLines(width, "Focused: "+observationTargetLabel(*r.FocusedTarget), palette.Secondary.On)...)
	}
	if r.VisibleText != "" {
		out = append(out, "")
		out = append(out, webResultLines(width, r.VisibleText, palette.Fg.On)...)
	}
	if len(r.Targets) > 0 {
		out = append(out, webResultLines(width, fmt.Sprintf("Targets: %d", len(r.Targets)), palette.Muted.On)...)
		for _, target := range r.Targets {
			out = append(out, webResultLines(width, observationTargetLabel(target), palette.Muted.On)...)
		}
	}
	for _, hint := range r.Hints {
		out = append(out, webResultLines(width, "Hint: "+hint, palette.Muted.On)...)
	}
	if len(r.Raw) > 0 {
		out = append(out, webResultLines(width, "Raw metadata available in tool output", palette.Muted.On)...)
	}
	return out
}

func observationTargetLabel(t computer.TargetDescriptor) string {
	label := t.Name
	if label == "" {
		label = t.Text
	}
	return strings.TrimSpace(t.ID + " · " + t.Role + " " + label)
}

// Recognize only the tools' known serialized forms; errors and unfamiliar
// payloads retain the generic renderer. Fetch already emits labelled plaintext.
func renderWebToolText(width int, name, text string) []string {
	switch strings.ToLower(name) {
	case "web_fetch":
		var decoded string
		if json.Unmarshal([]byte(text), &decoded) == nil {
			text = decoded
		}
		if !strings.HasPrefix(text, "fetched: ") {
			return nil
		}
		var out []string
		header := true
		for line := range strings.SplitSeq(text, "\n") {
			style := palette.Fg.On
			if line == "" {
				header = false
			}
			if header {
				switch {
				case strings.HasPrefix(line, "fetched: "):
					line = strings.TrimPrefix(line, "fetched: ")
					style = palette.Secondary.On
				case strings.HasPrefix(line, "title: "):
					line = strings.TrimPrefix(line, "title: ")
				case strings.HasPrefix(line, "warning: "):
					style = palette.Warning.On
				default:
					style = palette.Muted.On
				}
			}
			out = append(out, webResultLines(width, line, style)...)
		}
		return out
	case "web_search":
		var r struct {
			Query       *string         `json:"query"`
			Results     json.RawMessage `json:"results"`
			Hits        json.RawMessage `json:"hits"`
			Suggestions []string        `json:"suggestions"`
		}
		if json.Unmarshal([]byte(text), &r) != nil || r.Query == nil || r.Results == nil && r.Hits == nil {
			return nil
		}
		payload := r.Results
		if payload == nil {
			payload = r.Hits
		}
		var hits []search.Hit
		if json.Unmarshal(payload, &hits) != nil {
			return nil
		}
		return renderWebSearchResult(width, search.Result{Query: *r.Query, Hits: hits, Suggestions: r.Suggestions})
	case "computer_observe", "computer_act":
		var r struct {
			computer.Observation
			Surface *computer.SurfaceInfo `json:"surface"`
		}
		if json.Unmarshal([]byte(text), &r) != nil || r.Surface == nil {
			return nil
		}
		r.Observation.Surface = *r.Surface
		return renderComputerObservation(width, r.Observation)
	default:
		return nil
	}
}
