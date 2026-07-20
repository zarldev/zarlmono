package home_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/home"
)

func TestResolveBuildPromptDir(t *testing.T) {
	const embedded = "embedded core"

	tests := []struct {
		name      string
		files     map[string]string
		wantMode  home.PromptResolutionMode
		wantBody  string
		wantPrefs string
		wantDiag  string
		noPrefs   bool
	}{
		{
			name:      "absent optional files uses embedded core",
			wantMode:  home.PromptEmbeddedCore,
			wantBody:  embedded,
			noPrefs:   true,
			wantPrefs: "",
		},
		{
			name: "empty preferences are ignored",
			files: map[string]string{
				home.PreferencesFile: "\n\t\n",
			},
			wantMode: home.PromptEmbeddedCore,
			wantBody: embedded,
			noPrefs:  true,
		},
		{
			name: "literal preferences are additive",
			files: map[string]string{
				home.PreferencesFile: "Prefer concise responses. {{.WorkspaceRoot}} stays literal.",
			},
			wantMode:  home.PromptEmbeddedCore,
			wantBody:  embedded,
			wantPrefs: "Prefer concise responses. {{.WorkspaceRoot}} stays literal.",
		},
		{
			name: "explicit override wins and skips preferences",
			files: map[string]string{
				home.PreferencesFile:    "Prefer concise responses.",
				home.PromptOverrideFile: "custom override",
				home.LegacyPromptFile:   "legacy override",
			},
			wantMode: home.PromptExplicitOverride,
			wantBody: "custom override",
			noPrefs:  true,
			wantDiag: "preferences.md is skipped",
		},
		{
			name: "known seed legacy prompt is ignored",
			files: map[string]string{
				home.LegacyPromptFile: embedded,
			},
			wantMode: home.PromptEmbeddedCore,
			wantBody: embedded,
			noPrefs:  true,
			wantDiag: "matches a shipped seed",
		},
		{
			name: "empty legacy prompt is ignored",
			files: map[string]string{
				home.LegacyPromptFile: "  \n",
			},
			wantMode: home.PromptEmbeddedCore,
			wantBody: embedded,
			noPrefs:  true,
			wantDiag: "ignoring empty legacy",
		},
		{
			name: "unknown legacy prompt preserves full override behavior",
			files: map[string]string{
				home.PreferencesFile:  "Prefer concise responses.",
				home.LegacyPromptFile: "legacy custom",
			},
			wantMode: home.PromptLegacyOverride,
			wantBody: "legacy custom",
			noPrefs:  true,
			wantDiag: "using customized legacy prompt.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tt.files {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			before := snapshotFiles(t, dir)
			got := home.ResolveBuildPromptDir(dir, embedded)
			after := snapshotFiles(t, dir)

			if got.Mode != tt.wantMode {
				t.Fatalf("Mode = %s, want %s", got.Mode, tt.wantMode)
			}
			if got.Body != tt.wantBody {
				t.Fatalf("Body = %q, want %q", got.Body, tt.wantBody)
			}
			if tt.noPrefs {
				if got.UsePreferences {
					t.Fatalf("UsePreferences = true, want skipped")
				}
			} else if !got.UsePreferences || got.Preferences != tt.wantPrefs {
				t.Fatalf("preferences = active %v %q, want %q", got.UsePreferences, got.Preferences, tt.wantPrefs)
			}
			if tt.wantDiag != "" && !diagnosticsContain(got.Diagnostics, tt.wantDiag) {
				t.Fatalf("Diagnostics = %#v, want substring %q", got.Diagnostics, tt.wantDiag)
			}
			if before != after {
				t.Fatalf("resolution mutated files: before %q after %q", before, after)
			}
		})
	}
}

func TestResolveBuildPromptDirReportsReadErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, home.PreferencesFile), 0o700); err != nil {
		t.Fatal(err)
	}

	got := home.ResolveBuildPromptDir(dir, "embedded")
	if got.Mode != home.PromptEmbeddedCore || got.Body != "embedded" {
		t.Fatalf("resolution = %s %q, want embedded fallback", got.Mode, got.Body)
	}
	if !diagnosticsContain(got.Diagnostics, "read") || !diagnosticsContain(got.Diagnostics, home.PreferencesFile) {
		t.Fatalf("Diagnostics = %#v, want read diagnostic for preferences", got.Diagnostics)
	}
}

func TestIsKnownLegacyPromptSeedAcceptsCurrentDefaultBody(t *testing.T) {
	body := "current embedded body"
	if !home.IsKnownLegacyPromptSeed([]byte(body), body) {
		t.Fatal("exact current default body should be treated as an untouched seed")
	}
}

func diagnosticsContain(diags []string, want string) bool {
	for _, diag := range diags {
		if strings.Contains(diag, want) {
			return true
		}
	}
	return false
}

func snapshotFiles(t *testing.T, dir string) string {
	t.Helper()
	var out strings.Builder
	for _, name := range []string{home.PreferencesFile, home.PromptOverrideFile, home.LegacyPromptFile} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out.WriteString(name)
		out.WriteByte('=')
		out.Write(data)
		out.WriteByte('\n')
	}
	return out.String()
}
