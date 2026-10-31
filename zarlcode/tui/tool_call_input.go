package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Quoted assignments can occur inside commands and scripts as well as JSON.
// Redaction happens before JSON escaping; execution arguments remain untouched.
var toolInputAssignments = regexp.MustCompile(`(?i)(\b(?:password|passphrase|api[_-]?key|auth[_-]?token|access[_-]?token|refresh[_-]?token|client[_-]?secret|secret|token)\b["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"?|'[^']*'?|[^\s,;}\]]+)`)

var toolInputAuthorization = regexp.MustCompile(`(?i)((?:proxy[_-]?)?authorization["']?\s*[:=]\s*["']?)(?:basic|bearer)\s+[^"'\s,}]+`)
var toolInputURLCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@`)

func redactToolInputText(text string) string {
	// These mandatory literals avoid whole-preview regex scans on ordinary
	// command output. Authorization contains no non-ASCII simple-fold variants.
	if strings.Contains(strings.ToLower(text), "authorization") {
		text = toolInputAuthorization.ReplaceAllString(text, `${1}[REDACTED]`)
	}
	if strings.Contains(text, "://") {
		text = toolInputURLCredentials.ReplaceAllString(text, `${1}[REDACTED]@`)
	}
	if possibleToolInputAssignment(text) {
		text = toolInputAssignments.ReplaceAllString(text, `${1}"[REDACTED]"`)
	}
	return tools.RedactSecrets(text)
}

func possibleToolInputAssignment(text string) bool {
	// Keep Unicode folding (including long-s and Kelvin sign) in the regexp.
	for i := range len(text) {
		if text[i] >= 0x80 {
			return true
		}
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"password", "passphrase", "key", "token", "secret"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func escapeToolInputControls(text string) string {
	var out strings.Builder
	for _, char := range text {
		if unicode.Is(unicode.Bidi_Control, char) || unicode.IsControl(char) && char != '\n' && char != '\t' {
			fmt.Fprintf(&out, `\u%04x`, char)
		} else {
			out.WriteRune(char)
		}
	}
	return out.String()
}

func sensitiveToolInputKey(key string) bool {
	key = strings.Map(func(char rune) rune {
		if char >= 'A' && char <= 'Z' {
			return char + ('a' - 'A')
		}
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			return char
		}
		return -1
	}, key)
	if strings.Contains(key, "password") || strings.Contains(key, "passphrase") || strings.Contains(key, "secret") || strings.Contains(key, "privatekey") {
		return true
	}
	if strings.HasSuffix(key, "apikey") || strings.HasSuffix(key, "accesstoken") || strings.HasSuffix(key, "refreshtoken") || strings.HasSuffix(key, "authtoken") {
		return true
	}
	switch key {
	case "token", "idtoken", "authorization", "proxyauthorization", "cookie", "setcookie", "credentials":
		return true
	default:
		return false
	}
}

func redactToolInputValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			if sensitiveToolInputKey(key) {
				out[key] = "[REDACTED]"
			} else {
				out[key] = redactToolInputValue(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for index, child := range value {
			out[index] = redactToolInputValue(child)
		}
		return out
	case string:
		return redactToolInputText(value)
	default:
		return value
	}
}

// presentToolCall owns a safe, immutable display snapshot of the submitted call.
// It prefers the raw wire arguments and preserves numbers through decoding.
func presentToolCall(name string, parameters map[string]any, raw string) (string, string) {
	data := []byte(raw)
	if raw == "" {
		if parameters == nil {
			data = []byte(`{}`)
		} else {
			var err error
			data, err = json.Marshal(parameters)
			if err != nil {
				return "", `"[arguments unavailable]"`
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	decodeErr := decoder.Decode(&value)
	var extra any
	if decodeErr != nil || decoder.Decode(&extra) != io.EOF {
		// A malformed source cannot be structurally redacted safely. A
		// successful dispatch repair may still supply valid decoded arguments.
		if raw != "" && parameters != nil {
			return presentToolCall(name, parameters, "")
		}
		return "", `"[malformed arguments hidden for safety]"`
	}
	redacted := redactToolInputValue(value)
	formatted, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return "", `"[arguments unavailable]"`
	}
	var hint string
	if object, ok := redacted.(map[string]any); ok {
		hint = redactToolInputText(toolArgHint(name, object))
	}
	return escapeToolInputControls(hint), escapeToolInputControls(string(formatted))
}
