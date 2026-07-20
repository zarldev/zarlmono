package home_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zarldev/zarlmono/zarlcode/home"
)

func TestMaterialiseDoesNotCreatePromptFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	res, err := home.Materialise()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{home.LegacyPromptFile, home.PreferencesFile, home.PromptOverrideFile} {
		if _, err := os.Stat(filepath.Join(res.Dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s stat err = %v, want not exist", name, err)
		}
		if slices.Contains(res.Created, name) || slices.Contains(res.Existed, name) {
			t.Fatalf("Result mentions %s: created=%v existed=%v", name, res.Created, res.Existed)
		}
	}
	for _, name := range []string{"skills/", "tools/", "hooks/"} {
		if !slices.Contains(res.Created, name) {
			t.Fatalf("Created = %v, want %s", res.Created, name)
		}
	}
}

func TestMaterialiseLeavesExistingPromptFilesByteIdentical(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir, err := home.RootPromptPath()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(dir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		home.LegacyPromptFile:   "legacy custom",
		home.PreferencesFile:    "prefer terse",
		home.PromptOverrideFile: "full override",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	res, err := home.Materialise()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(res.Created, home.LegacyPromptFile) || slices.Contains(res.Existed, home.LegacyPromptFile) {
		t.Fatalf("Result should not account prompt files: %#v", res)
	}
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", name, data, want)
		}
	}
}

func TestMaterialiseIsIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first, err := home.Materialise()
	if err != nil {
		t.Fatal(err)
	}
	second, err := home.Materialise()
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Created) != 0 {
		t.Fatalf("second Created = %v, want empty", second.Created)
	}
	if len(second.Existed) != len(first.Created) {
		t.Fatalf("second Existed = %v, want %d entries", second.Existed, len(first.Created))
	}
}
