package home

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/db"
)

const (
	// LegacyPromptFile is the old full system-prompt override. New installs do
	// not create it; existing customized files remain active for compatibility.
	LegacyPromptFile = "prompt.md"
	// PreferencesFile is the additive, literal global guidance file.
	PreferencesFile = "preferences.md"
	// PromptOverrideFile is the explicit advanced full system-prompt override.
	PromptOverrideFile = "prompt.override.md"

	// RootPromptFile is retained for callers and user docs that still refer to
	// the legacy filename. Prefer LegacyPromptFile in new code.
	RootPromptFile = LegacyPromptFile
)

// PromptResolutionMode names the source selected for the BUILD-mode prompt body.
type PromptResolutionMode string

const (
	// PromptEmbeddedCore means zarlcode is using the embedded system prompt.
	PromptEmbeddedCore PromptResolutionMode = "embedded_core"
	// PromptExplicitOverride means prompt.override.md replaced the build prompt.
	PromptExplicitOverride PromptResolutionMode = "explicit_override"
	// PromptLegacyOverride means a customized legacy prompt.md replaced the build prompt.
	PromptLegacyOverride PromptResolutionMode = "legacy_override"
)

// PromptResolution describes the per-user prompt files that affect live prompt
// assembly. Body is the BUILD-mode body; plan mode and named sub-agents still
// use their own bodies but may consume Preferences.
type PromptResolution struct {
	Mode              PromptResolutionMode
	Body              string
	BodySource        string
	Preferences       string
	PreferencesSource string
	UsePreferences    bool
	Diagnostics       []string
}

// RootPromptPath returns the absolute path of the legacy full prompt override
// (~/.zarlcode/prompt.md). New installs no longer create this file; existing
// customized files remain active through ResolveBuildPrompt for compatibility.
func RootPromptPath() (string, error) { return promptFilePath(LegacyPromptFile) }

// PreferencesPath returns the absolute path of the additive global preferences
// file (~/.zarlcode/preferences.md).
func PreferencesPath() (string, error) { return promptFilePath(PreferencesFile) }

// PromptOverridePath returns the absolute path of the explicit full prompt
// override (~/.zarlcode/prompt.override.md).
func PromptOverridePath() (string, error) { return promptFilePath(PromptOverrideFile) }

func promptFilePath(file string) (string, error) {
	dir, err := db.DefaultDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(dir, file), nil
}

// ResolveBuildPrompt resolves the BUILD-mode prompt against ~/.zarlcode.
func ResolveBuildPrompt(defaultBody string) PromptResolution {
	dir, err := db.DefaultDir()
	if err != nil {
		return PromptResolution{
			Mode:       PromptEmbeddedCore,
			Body:       defaultBody,
			BodySource: "embedded system prompt",
			Diagnostics: []string{
				fmt.Sprintf("prompt: resolve ~/.zarlcode: %v; using embedded system prompt", err),
			},
		}
	}
	return ResolveBuildPromptDir(dir, defaultBody)
}

// ResolveBuildPromptDir resolves the BUILD-mode prompt against dir. It is the
// testable form of ResolveBuildPrompt and never creates, rewrites, renames, or
// deletes files.
func ResolveBuildPromptDir(dir, defaultBody string) PromptResolution {
	res := PromptResolution{
		Mode:       PromptEmbeddedCore,
		Body:       defaultBody,
		BodySource: "embedded system prompt",
	}

	prefsPath := filepath.Join(dir, PreferencesFile)
	if data, ok, diag := readPromptFile(prefsPath); diag != "" {
		res.Diagnostics = append(res.Diagnostics, diag)
	} else if ok && strings.TrimSpace(string(data)) != "" {
		res.Preferences = string(data)
		res.PreferencesSource = prefsPath
		res.UsePreferences = true
	}

	overridePath := filepath.Join(dir, PromptOverrideFile)
	if data, ok, diag := readPromptFile(overridePath); diag != "" {
		res.Diagnostics = append(res.Diagnostics, diag)
	} else if ok && strings.TrimSpace(string(data)) != "" {
		res.Mode = PromptExplicitOverride
		res.Body = string(data)
		res.BodySource = overridePath
		res.UsePreferences = false
		if res.Preferences != "" {
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("prompt: %s is active; %s is skipped for BUILD-mode full-override semantics", PromptOverrideFile, PreferencesFile))
		}
		return res
	}

	legacyPath := filepath.Join(dir, LegacyPromptFile)
	if data, ok, diag := readPromptFile(legacyPath); diag != "" {
		res.Diagnostics = append(res.Diagnostics, diag)
	} else if ok {
		switch {
		case strings.TrimSpace(string(data)) == "":
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("prompt: ignoring empty legacy %s", LegacyPromptFile))
		case IsKnownLegacyPromptSeed(data, defaultBody):
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("prompt: ignoring legacy %s because it matches a shipped seed; move durable custom guidance to %s", LegacyPromptFile, PreferencesFile))
		default:
			res.Mode = PromptLegacyOverride
			res.Body = string(data)
			res.BodySource = legacyPath
			res.UsePreferences = false
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("prompt: using customized legacy %s as a full BUILD-mode override; migrate additive guidance to %s or rename the full override to %s", LegacyPromptFile, PreferencesFile, PromptOverrideFile))
		}
	}

	return res
}

func readPromptFile(path string) ([]byte, bool, string) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return data, true, ""
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, ""
	default:
		return nil, false, fmt.Sprintf("prompt: read %s: %v", path, err)
	}
}

// IsKnownLegacyPromptSeed reports whether data is an untouched prompt.md seeded
// from a shipped embedded prompt. The current default body is accepted directly
// so development builds and future releases do not need to add their own hash to
// the static migration list just to ignore an exact generated copy.
func IsKnownLegacyPromptSeed(data []byte, defaultBody string) bool {
	if bytes.Equal(data, []byte(defaultBody)) {
		return true
	}
	sum := sha256.Sum256(data)
	_, ok := knownLegacyPromptSeedHashes[hex.EncodeToString(sum[:])]
	return ok
}
