package docmedia_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/docmedia"
)

func TestSyncThenCheck(t *testing.T) {
	root := t.TempDir()
	writeCanonicalFiles(t, root)

	if err := docmedia.Sync(root); err != nil {
		t.Fatal(err)
	}
	if err := docmedia.Check(root); err != nil {
		t.Fatal(err)
	}
}

func TestCheckReportsDivergentPublicGIF(t *testing.T) {
	root := t.TempDir()
	writeCanonicalFiles(t, root)
	if err := docmedia.Sync(root); err != nil {
		t.Fatal(err)
	}
	mapping := docmedia.Mappings()[0]
	if err := os.WriteFile(filepath.Join(root, mapping.Public), []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := docmedia.Check(root)
	if err == nil || !strings.Contains(err.Error(), mapping.Public) {
		t.Fatalf("Check() error = %v, want mapping for %s", err, mapping.Public)
	}
}

func writeCanonicalFiles(t *testing.T, root string) {
	t.Helper()
	for _, mapping := range docmedia.Mappings() {
		path := filepath.Join(root, mapping.Canonical)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(mapping.Canonical), 0o644); err != nil {
			t.Fatal(err)
		}
		publicDir := filepath.Dir(filepath.Join(root, mapping.Public))
		if err := os.MkdirAll(publicDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
