package engine_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const tuiImport = "github.com/zarldev/zarlmono/zarlcode/tui"

func TestEngineDoesNotImportTUI(t *testing.T) {
	t.Parallel()
	violations := importsOf(t, engineGoSources(t), tuiImport)
	if len(violations) != 0 {
		t.Fatalf("engine must not depend on TUI:\n%s", strings.Join(violations, "\n"))
	}
}

func TestEngineImportBoundaryChecker(t *testing.T) {
	t.Parallel()
	sources := map[string]string{
		"allowed.go":   `package engine; import "github.com/zarldev/zarlmono/zkit/ai/llm"`,
		"forbidden.go": `package engine; import ui "` + tuiImport + `"; var _ ui.Model`,
	}
	violations := importsOf(t, sources, tuiImport)
	if len(violations) != 1 || !strings.Contains(violations[0], "forbidden.go") {
		t.Fatalf("violations = %v, want positioned TUI import", violations)
	}
}

func importsOf(t *testing.T, sources map[string]string, forbidden string) []string {
	t.Helper()
	var violations []string
	for name, source := range sources {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, source, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("%s: decode import: %v", fset.Position(imported.Pos()), err)
			}
			if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
				violations = append(violations, fmt.Sprintf("%s: engine must not import %s", fset.Position(imported.Pos()), path))
			}
		}
	}
	sort.Strings(violations)
	return violations
}

func engineGoSources(t *testing.T) map[string]string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate engine test package")
	}
	root := filepath.Dir(current)
	sources := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sources[entry.Name()] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sources
}
