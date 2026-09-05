package prefs_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const zarlcodeModuleImport = "github.com/zarldev/zarlmono/zarlcode"

func TestSharedPreferenceArchitecture(t *testing.T) {
	t.Parallel()
	root := zkitModuleRoot(t)
	violations := sharedArchitectureViolations(t, zkitProductionSources(t, root))
	if len(violations) > 0 {
		t.Fatalf("zkit architecture violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestSharedPreferenceArchitectureChecker(t *testing.T) {
	t.Parallel()
	allowed := map[string]string{
		"prefs/keys.go": `package prefs; const credentialProtectionSetting = "credential_protection"; const KeyVersion = 2; type KeyValue struct{}`,
		"runner/run.go": `package runner; import "github.com/zarldev/zarlmono/zkit/prefs"; var _ prefs.KeyValue`,
	}
	if got := sharedArchitectureViolations(t, allowed); len(got) != 0 {
		t.Fatalf("allowed sources rejected: %v", got)
	}
	for name, source := range map[string]string{
		"zarlcode import": `package prefs; import app "` + zarlcodeModuleImport + `/prefs"; var _ = app.KeyModel`,
		"app key":         `package prefs; const KeyTheme = "theme"`,
		"model type":      `package prefs; type ModelSelection struct{ Model string }`,
		"model method":    `package prefs; type Service struct{}; func (*Service) SetModelSelection() {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := "prefs/violation.go"
			if name == "zarlcode import" {
				path = "runner/violation.go"
			}
			got := sharedArchitectureViolations(t, map[string]string{path: source})
			if len(got) != 1 || !strings.Contains(got[0], path) {
				t.Fatalf("violations = %v, want positioned %s violation", got, name)
			}
		})
	}
}

func sharedArchitectureViolations(t *testing.T, sources map[string]string) []string {
	t.Helper()
	var violations []string
	for name, source := range sources {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, source, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("%s: decode import: %v", fset.Position(imported.Pos()), err)
			}
			if path == zarlcodeModuleImport || strings.HasPrefix(path, zarlcodeModuleImport+"/") {
				violations = append(violations, fmt.Sprintf("%s: zkit must not import application module %s", fset.Position(imported.Pos()), path))
			}
		}
		if !strings.HasPrefix(filepath.ToSlash(name), "prefs/") {
			continue
		}
		for _, declaration := range file.Decls {
			switch decl := declaration.(type) {
			case *ast.GenDecl:
				for _, rawSpec := range decl.Specs {
					switch spec := rawSpec.(type) {
					case *ast.TypeSpec:
						if spec.Name.Name == "ModelSelection" {
							violations = append(violations, fmt.Sprintf("%s: application model selection belongs in zarlcode/prefs", fset.Position(spec.Pos())))
						}
					case *ast.ValueSpec:
						if decl.Tok != token.CONST {
							continue
						}
						for _, identifier := range spec.Names {
							if ast.IsExported(identifier.Name) && strings.HasPrefix(identifier.Name, "Key") && identifier.Name != "KeyVersion" {
								violations = append(violations, fmt.Sprintf("%s: exported application key %s belongs in zarlcode/prefs", fset.Position(identifier.Pos()), identifier.Name))
							}
						}
					}
				}
			case *ast.FuncDecl:
				if decl.Name.Name == "SetModelSelection" {
					violations = append(violations, fmt.Sprintf("%s: application model transition belongs in zarlcode/prefs", fset.Position(decl.Name.Pos())))
				}
			}
		}
	}
	slices.Sort(violations)
	return violations
}

func zkitModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test source")
	}
	return filepath.Dir(filepath.Dir(file))
}

func zkitProductionSources(t *testing.T, root string) map[string]string {
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
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
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
