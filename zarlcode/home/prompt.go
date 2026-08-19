package home

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zarldev/zarlmono/zkit/db"
)

const (
	// PreferencesFile is the additive, literal global guidance file.
	PreferencesFile = "preferences.md"
	// PromptOverrideFile is the explicit advanced full system-prompt override.
	PromptOverrideFile = "prompt.override.md"
)

// PromptResolutionMode names the source selected for the BUILD-mode prompt body.
type PromptResolutionMode string

const (
	// PromptEmbeddedCore means zarlcode is using the embedded system prompt.
	PromptEmbeddedCore PromptResolutionMode = "embedded_core"
	// PromptExplicitOverride means prompt.override.md replaced the build prompt.
	PromptExplicitOverride PromptResolutionMode = "explicit_override"
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
