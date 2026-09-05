package prefs_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	sharedPrefsImport = "github.com/zarldev/zarlmono/zkit/prefs"
	appPrefsImport    = "github.com/zarldev/zarlmono/zarlcode/prefs"
)

func TestPreferenceImportBoundary(t *testing.T) {
	t.Parallel()
	root := filepath.Dir(packageDir(t))
	violations := directSharedPreferenceImports(t, moduleGoSources(t, root))
	if len(violations) > 0 {
		t.Fatalf("zarlcode preference boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestPreferenceImportBoundaryChecker(t *testing.T) {
	t.Parallel()
	allowed := map[string]string{
		"prefs/catalog.go":   `package prefs; import shared "` + sharedPrefsImport + `"; var _ = shared.ScopeGlobal`,
		"engine/settings.go": `package engine; import appprefs "` + appPrefsImport + `"; var _ = appprefs.KeyModel`,
	}
	if got := directSharedPreferenceImports(t, allowed); len(got) != 0 {
		t.Fatalf("allowed sources rejected: %v", got)
	}
	for name, source := range map[string]string{
		"aliased": `package engine; import generic "` + sharedPrefsImport + `"; var _ = generic.ScopeGlobal`,
		"blank":   `package tui; import _ "` + sharedPrefsImport + `"`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := directSharedPreferenceImports(t, map[string]string{"engine/bypass_test.go": source})
			if len(got) != 1 || !strings.Contains(got[0], "engine/bypass_test.go") {
				t.Fatalf("violations = %v, want positioned direct-import violation", got)
			}
		})
	}
}

func directSharedPreferenceImports(t *testing.T, sources map[string]string) []string {
	t.Helper()
	var violations []string
	for name, source := range sources {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, source, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		allowedFacade := strings.HasPrefix(filepath.ToSlash(name), "prefs/")
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("%s: decode import: %v", fset.Position(imported.Pos()), err)
			}
			if (path == sharedPrefsImport || strings.HasPrefix(path, sharedPrefsImport+"/")) && !allowedFacade {
				violations = append(violations, fmt.Sprintf("%s: import %s through zarlcode/prefs instead", fset.Position(imported.Pos()), path))
			}
		}
	}
	slices.Sort(violations)
	return violations
}

func moduleGoSources(t *testing.T, root string) map[string]string {
	t.Helper()
	sources := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == "vendor" || entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sources[filepath.ToSlash(relative)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sources
}
