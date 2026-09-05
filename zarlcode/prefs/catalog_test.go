package prefs_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	appprefs "github.com/zarldev/zarlmono/zarlcode/prefs"
	"github.com/zarldev/zarlmono/zkit/db"
)

type keyFixture struct {
	BaselineRevision string            `json:"baseline_revision"`
	Notes            string            `json:"notes"`
	Keys             map[string]string `json:"keys"`
}

func TestPersistedKeyCompatibility(t *testing.T) {
	t.Parallel()
	dir := packageDir(t)
	fixture := readKeyFixture(t, filepath.Join(dir, "testdata", "persisted_keys.json"))
	actual := exportedKeyConstants(t, dir)
	var problems []string
	for name, want := range fixture.Keys {
		got, ok := actual[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("removed %s (persisted as %q)", name, want))
			continue
		}
		if got != want {
			problems = append(problems, fmt.Sprintf("changed %s from %q to %q", name, want, got))
		}
	}
	for name, value := range actual {
		if _, ok := fixture.Keys[name]; !ok {
			problems = append(problems, fmt.Sprintf("uncovered %s = %q", name, value))
		}
	}
	slices.Sort(problems)
	if len(problems) > 0 {
		t.Fatalf("persisted preference compatibility mismatch:\n%s", strings.Join(problems, "\n"))
	}
}

func readKeyFixture(t *testing.T, path string) keyFixture {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var fixture keyFixture
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if fixture.BaselineRevision == "" || fixture.Notes == "" || len(fixture.Keys) == 0 {
		t.Fatal("fixture requires provenance and keys")
	}
	return fixture
}

func exportedKeyConstants(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	keys := make(map[string]string)
	values := make(map[string]string)
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, file.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			decl, ok := declaration.(*ast.GenDecl)
			if !ok || decl.Tok != token.CONST {
				continue
			}
			for _, rawSpec := range decl.Specs {
				spec := rawSpec.(*ast.ValueSpec)
				for i, name := range spec.Names {
					if !ast.IsExported(name.Name) || !strings.HasPrefix(name.Name, "Key") {
						continue
					}
					if len(spec.Values) != len(spec.Names) {
						t.Fatalf("%s: %s must have an explicit independent string value", fset.Position(name.Pos()), name.Name)
					}
					literal, ok := spec.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Fatalf("%s: %s uses unsupported non-string-literal value", fset.Position(name.Pos()), name.Name)
					}
					value, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatalf("%s: decode %s: %v", fset.Position(name.Pos()), name.Name, err)
					}
					if prior, exists := keys[name.Name]; exists {
						t.Fatalf("%s: duplicate constant %s (already %q)", fset.Position(name.Pos()), name.Name, prior)
					}
					if prior, exists := values[value]; exists {
						t.Fatalf("%s: %s duplicates persisted value owned by %s", fset.Position(name.Pos()), name.Name, prior)
					}
					keys[name.Name] = value
					values[value] = name.Name
				}
			}
		}
	}
	return keys
}

func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate preference test source")
	}
	return filepath.Dir(file)
}

func TestSetModelSelectionOwnsApplicationKeys(t *testing.T) {
	t.Parallel()
	store, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := appprefs.NewService(store, nil, t.TempDir())
	if err := svc.SetModelSelection(t.Context(), appprefs.ScopeWorkspace, appprefs.ModelSelection{
		Provider: "openai", Model: "gpt-test",
	}); err != nil {
		t.Fatal(err)
	}
	provider, err := svc.GetSetting(t.Context(), appprefs.ScopeWorkspace, appprefs.KeyProvider)
	if err != nil {
		t.Fatal(err)
	}
	model, err := svc.GetSetting(t.Context(), appprefs.ScopeWorkspace, appprefs.KeyModel)
	if err != nil {
		t.Fatal(err)
	}
	if provider.Value != "openai" || model.Value != "gpt-test" {
		t.Fatalf("selection = %q/%q", provider.Value, model.Value)
	}
	if err := svc.SetModelSelection(t.Context(), appprefs.ScopeWorkspace, appprefs.ModelSelection{Provider: "ollama"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetSetting(t.Context(), appprefs.ScopeWorkspace, appprefs.KeyModel); !errors.Is(err, appprefs.ErrNotFound) {
		t.Fatalf("cleared model = %v, want ErrNotFound", err)
	}
}
