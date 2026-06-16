// Package repair tries to recover usable JSON from malformed
// model output. The model is the canonical source of failure here:
// small models (Qwen3.5-9B, Gemma 3, Qwen3.6-35B-A3B) routinely emit
// tool-call argument JSON with literal newlines inside string values,
// trailing commas, single quotes, unquoted keys, or missing closing
// braces (when max_tokens truncates mid-object). A strict
// json.Unmarshal rejects all of these and the model has to figure out
// the recovery from the resulting error message alone — which it
// frequently can't.
//
// Repair runs a fixed cascade of transformations, ordered from most
// likely to least invasive, and tries to Unmarshal after each step.
// The first successful parse wins. The test corpus doubles as the
// regression set for the cascade's ordering — reorder steps only with
// it green.
//
// Repair is opinionated about JSON object inputs — tool-call argument
// schemas are always objects. It will succeed on array or scalar
// inputs too, but the cascade is tuned for object recovery.
package repair

import (
	"bytes"
	"encoding/json"
	"regexp"
)

// Unmarshal is the convenience wrapper most callers want. It tries a
// strict json.Unmarshal first; on failure it runs Repair and retries.
// On total failure it returns the *strict* parse error, since the
// repaired-output error would describe the repair attempt rather
// than the original malformed input.
//
// A nil or empty raw slice is treated as an empty object and
// Unmarshals to dst as such — same shape as the runner's previous
// "skip Unmarshal if Arguments == \"\"" branch.
func Unmarshal(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.Unmarshal([]byte("{}"), dst)
	}
	if err := json.Unmarshal(raw, dst); err == nil {
		return nil
	} else {
		repaired, ok := Repair(raw)
		if !ok {
			return err
		}
		if err2 := json.Unmarshal(repaired, dst); err2 == nil {
			return nil
		}
		return err
	}
}

// Repair walks the recovery cascade and returns the first
// successfully-parseable byte slice along with ok=true. When nothing
// in the cascade produces valid JSON it returns the original input
// and ok=false so callers can fall back to whatever they had.
//
// Cascade (each step attempted in order, exits early on success):
//
//  1. Trim + direct parse. Many "broken" inputs are actually fine
//     once leading/trailing whitespace is stripped.
//  2. Escape literal newlines / tabs / carriage returns that appear
//     INSIDE string values. Small models very commonly emit raw
//     newlines inside `"content": "...\n..."` argument values; a
//     strict parser rejects, but the data is recoverable.
//  3. Strip trailing commas before `}` and `]`. Common in
//     Python-trained models that learned trailing-comma habits.
//  4. Single-quote → double-quote conversion. Only attempted when
//     the input contains no double quotes at all (so we don't
//     corrupt valid JSON strings that legitimately contain `'`).
//  5. Quote unquoted object keys. Conservative: only fires when the
//     input has no quoted keys yet, so we don't double-quote
//     already-quoted ones.
//  6. Balance missing closing braces / brackets. Runs at the end
//     because earlier steps may legitimately introduce new braces.
//  7. Extract the first flat `{...}` substring as a last-ditch.
//     Only catches non-nested objects; nested objects need the
//     earlier repair steps to succeed.
func Repair(raw []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, false
	}
	trimmed := bytes.TrimSpace(raw)
	if isParseable(trimmed) {
		return trimmed, true
	}

	candidate := escapeControlCharsInStrings(trimmed)
	if isParseable(candidate) {
		return candidate, true
	}

	candidate = stripTrailingCommas(candidate)
	if isParseable(candidate) {
		return candidate, true
	}

	if !bytes.ContainsAny(candidate, "\"") && bytes.ContainsAny(candidate, "'") {
		candidate = bytes.ReplaceAll(candidate, []byte("'"), []byte("\""))
		if isParseable(candidate) {
			return candidate, true
		}
	}

	// Only attempt unquoted-key fixup when no quoted-key markers
	// appear; bytes.Contains is cheap and avoids the regex pass
	// when the input is already well-quoted.
	if !bytes.Contains(candidate, []byte(`":`)) {
		candidate = unquotedKeyRe.ReplaceAll(candidate, []byte(`${1}"${2}"${3}`))
		if isParseable(candidate) {
			return candidate, true
		}
	}

	candidate = balanceClosers(candidate)
	if isParseable(candidate) {
		return candidate, true
	}

	if m := flatObjectRe.Find(candidate); m != nil && isParseable(m) {
		return m, true
	}

	return raw, false
}

// isParseable returns true when raw decodes as valid JSON (any
// shape — object, array, number, string, bool, null). Cheap: it
// only Unmarshals into an empty interface and discards the result.
func isParseable(raw []byte) bool {
	var v any
	return json.Unmarshal(raw, &v) == nil
}

// escapeControlCharsInStrings walks raw and escapes literal newlines,
// carriage returns, and tabs that appear inside JSON string literals.
// State machine: a `"` flips inString unless preceded by an unescaped
// `\`. Backslash-quoted pairs (`\"`, `\\`, etc.) are passed through
// verbatim so we don't double-escape.
//
// Outside strings, control chars are preserved — they're whitespace
// in JSON and harmless. We only care about the in-string case
// because that's the input shape a strict parser rejects.
func escapeControlCharsInStrings(raw []byte) []byte {
	out := make([]byte, 0, len(raw)+16)
	inString := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString && ch == '\\' && i+1 < len(raw) {
			// Pass through the escape sequence verbatim.
			out = append(out, ch, raw[i+1])
			i++
			continue
		}
		if ch == '"' {
			inString = !inString
			out = append(out, ch)
			continue
		}
		if !inString {
			out = append(out, ch)
			continue
		}
		switch ch {
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, ch)
		}
	}
	return out
}

// stripTrailingCommas removes commas that sit between an array/object
// element and its closer (e.g. `[1,2,]` → `[1,2]`). Uses literal
// regex replace rather than per-byte scanning because the patterns
// are simple and the input is small.
func stripTrailingCommas(raw []byte) []byte {
	raw = trailingCommaObjectRe.ReplaceAll(raw, []byte("}"))
	raw = trailingCommaArrayRe.ReplaceAll(raw, []byte("]"))
	return raw
}

// balanceClosers walks raw, tracking unclosed openers on a stack
// (string-aware so `"foo}"` doesn't confuse the count), and appends
// the right closers in reverse stack order at the end. Done this way
// because a naive "append N `}` then N `]`" produces `{"xs":[1,2,3}]`
// for `{"xs":[1,2,3`, which doesn't parse — the array must close
// before the object that contains it.
//
// A string left open at the end is closed first — max_tokens usually
// truncates mid-string-value (`{"path": "/foo/ba`), not neatly
// between tokens, and no closer sequence can rescue an unterminated
// string.
//
// Mismatched closers (`[ }`) are ignored on pop; the trailing closer
// append finishes whatever's left on the stack. Inputs that mismatch
// this badly usually fail the final isParseable check upstream
// anyway, so we don't try to repair them further.
func balanceClosers(raw []byte) []byte {
	stack := make([]byte, 0, 8)
	inString := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString && ch == '\\' && i+1 < len(raw) {
			i++
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{', '[':
			stack = append(stack, ch)
		case '}':
			if n := len(stack); n > 0 && stack[n-1] == '{' {
				stack = stack[:n-1]
			}
		case ']':
			if n := len(stack); n > 0 && stack[n-1] == '[' {
				stack = stack[:n-1]
			}
		}
	}
	if inString {
		raw = append(raw, '"')
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			raw = append(raw, '}')
		} else {
			raw = append(raw, ']')
		}
	}
	return raw
}

// trailingCommaObjectRe matches `,` followed by optional whitespace
// then `}`. The replacement drops the `,` and preserves only `}` —
// the inter-comma whitespace is sacrificed because it carries no
// information.
var trailingCommaObjectRe = regexp.MustCompile(`,\s*\}`)
var trailingCommaArrayRe = regexp.MustCompile(`,\s*\]`)

// unquotedKeyRe matches an unquoted JSON-object key: opener (`{` or
// `,`) + optional whitespace + identifier + optional whitespace +
// `:`. Identifier characters mirror Go's identifier rules
// (`[A-Za-z_][A-Za-z0-9_]*`); the rare JSON.5-style hyphenated key
// would still need quoting upstream.
//
// Wrapping with `(` `)` captures the opener/identifier/colon so the
// replacement string can re-emit them with quotes around the
// identifier (mirrors fallback.go's gemma4KeyRe approach).
var unquotedKeyRe = regexp.MustCompile(`([\{,]\s*)([A-Za-z_][A-Za-z0-9_]*)(\s*:)`)

// flatObjectRe matches the first non-nested `{...}` substring. Used
// as a last-ditch extraction when the surrounding text is garbage
// but contains an embedded simple object. Nested objects miss this
// path on purpose — the earlier repair steps cover them.
var flatObjectRe = regexp.MustCompile(`\{[^{}]*\}`)

// String is a convenience for callers holding a string instead of a
// []byte. Repaired output stays a string for symmetry.
func String(raw string) (string, bool) {
	b, ok := Repair([]byte(raw))
	return string(b), ok
}
