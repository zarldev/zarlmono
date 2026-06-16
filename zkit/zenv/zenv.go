// Package zenv reads environment-variable overrides with typed
// defaults. The pattern shows up in every CLI in this repo —
// "if env var present, parse it, else use the default" — and was
// previously open-coded in five places (zarlcode,
// cmd/agent-self-build, cmd/agent-demo, plus two flavours inside
// pkg/ai/config). One package, generic core, named wrappers for the
// common types.
//
// # Generic core
//
// Get is the typed primitive. Callers pass a default and a parser:
//
//	port    := zenv.Get("PORT", 8080, strconv.Atoi)
//	timeout := zenv.Get("TIMEOUT", 5*time.Second, time.ParseDuration)
//
// When the env var is unset OR the parser returns an error, the
// default is returned. Failure-as-default is deliberate — env config
// should be lenient at startup; loud schema validation belongs to the
// caller, not here.
//
// # Named wrappers
//
// For built-in types you'd normally pair with a stdlib parser, the
// named wrappers skip the explicit parse argument:
//
//	port    := zenv.Int("PORT", 8080)
//	debug   := zenv.Bool("DEBUG", false)
//	timeout := zenv.Duration("TIMEOUT", 5*time.Second)
//	name    := zenv.String("NAME", "anon")
//
// These are thin shims around Get with the obvious parser baked in.
// Use Get directly when you have your own type (URL, custom enum,
// etc.) and want the same default-on-failure semantics.
package zenv

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrUnset is returned by [MustGet] when the named environment
// variable is not present (or set to the empty string). Wrapped in
// the returned error so callers can `errors.Is(err, zenv.ErrUnset)`
// to distinguish "not configured" from "configured but invalid".
var ErrUnset = errors.New("zenv: env var unset")

// Get reads key from the environment, runs parse on the value, and
// falls back to def when the key is unset or parse returns an error.
// The generic primitive — every named wrapper in this package is a
// one-liner over Get.
func Get[T any](key string, def T, parse func(string) (T, error)) T {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := parse(raw)
	if err != nil {
		return def
	}
	return v
}

// MustGet is the strict counterpart to [Get]: it returns an error
// when the env var is unset or fails to parse, instead of silently
// falling back to a default. "Must" here applies to the env var
// (it must be set and valid) — not to the call semantics; failures
// surface as returned errors, not panics, so the caller decides
// whether to fatal, log, or fall through to another source.
//
// Use when downstream code can't safely proceed with a default —
// secrets, base64-encoded master keys, production-only feature
// flags, anything where a wrong-format value should halt startup
// instead of being silently replaced by a placeholder. For "optional
// but if present must be valid" cases, callers can layer their own
// logic on top: check ErrUnset to fall through, but surface parse
// errors loudly.
//
//	key, err := zenv.MustGet(masterKeyEnv, base64.StdEncoding.DecodeString)
//	if errors.Is(err, zenv.ErrUnset) { /* fall through to file */ }
//	if err != nil { return fmt.Errorf("master key: %w", err) }
func MustGet[T any](key string, parse func(string) (T, error)) (T, error) {
	raw := os.Getenv(key)
	if raw == "" {
		var zero T
		return zero, fmt.Errorf("%s: %w", key, ErrUnset)
	}
	v, err := parse(raw)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

// String returns the env value as a string, or def when unset. The
// "parse" step is identity, so this is mostly here for symmetry with
// the other typed wrappers.
func String(key, def string) string { return Get(key, def, identity) }

// Int returns the env value parsed as a base-10 int, or def when
// unset / unparseable.
func Int(key string, def int) int { return Get(key, def, strconv.Atoi) }

// Int64 returns the env value parsed as a 64-bit int.
func Int64(key string, def int64) int64 { return Get(key, def, parseInt64) }

// Float64 returns the env value parsed as a 64-bit float.
func Float64(key string, def float64) float64 { return Get(key, def, parseFloat64) }

// Duration returns the env value parsed by time.ParseDuration
// ("5s", "1m30s", "200ms"). Common pattern for timeouts.
func Duration(key string, def time.Duration) time.Duration {
	return Get(key, def, time.ParseDuration)
}

// Bool returns the env value parsed liberally as a boolean.
//
//	True : 1, t, true, yes, y, on   (case-insensitive)
//	False: 0, f, false, no, n, off  (case-insensitive)
//
// Anything else returns def. Wider than strconv.ParseBool because
// operators reasonably expect "yes/no" and "on/off" to work in env
// vars; strconv only accepts "1, t, true / 0, f, false".
func Bool(key string, def bool) bool { return Get(key, def, parseBool) }

func identity(s string) (string, error)      { return s, nil }
func parseInt64(s string) (int64, error)     { return strconv.ParseInt(s, 10, 64) }
func parseFloat64(s string) (float64, error) { return strconv.ParseFloat(s, 64) }

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	case "0", "f", "false", "no", "n", "off":
		return false, nil
	default:
		return false, errInvalidBool
	}
}

var errInvalidBool = errors.New("zenv: not a boolean")
