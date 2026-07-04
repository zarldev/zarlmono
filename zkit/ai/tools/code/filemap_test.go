package code_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
	"github.com/zarldev/zarlmono/zkit/ai/tools/code"
)

func TestFileMapToolMapsGoDeclarationsDeterministically(t *testing.T) {
	root := t.TempDir()
	writeFileMapFixture(t, root, "main.go", `package main

import (
	"fmt"
	alias "io"
)

const version string = "dev"
var count int

type Thing struct { Name string }
type Reader interface { Read([]byte) (int, error) }

func NewThing(name string) *Thing { return &Thing{Name: name} }
func (t *Thing) Print() { fmt.Println(t.Name) }
`)
	writeFileMapFixture(t, root, "main_test.go", `package main

func TestThing() {}
`)

	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	res, err := code.NewFileMapTool(ws).Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("tool failed: %s", res.Error)
	}
	out := res.Data.(code.FileMapResult).String()
	for _, want := range []string{
		"file_map: 1 file(s)  pattern: *.go",
		"main.go  package main",
		"imports: \"fmt\", alias \"io\"",
		"const version :: string",
		"var count :: int",
		"struct Thing :: struct",
		"interface Reader :: interface",
		"func NewThing :: func NewThing(name string) *Thing",
		"method (*Thing).Print :: func (t *Thing) Print()",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("file_map output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TestThing") {
		t.Fatalf("test file should be excluded by default:\n%s", out)
	}
}

func TestFileMapToolJSONIncludesTestsWhenRequested(t *testing.T) {
	root := t.TempDir()
	writeFileMapFixture(t, root, "main.go", "package main\nfunc Main() {}\n")
	writeFileMapFixture(t, root, "main_test.go", "package main\nfunc TestMain() {}\n")

	ws, err := code.NewWorkspace(root)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	res, err := code.NewFileMapTool(ws).Execute(t.Context(), tools.ToolCall{Arguments: tools.ToolParameters{
		"include_tests": true,
		"output":        "json",
	}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := res.Data.(code.FileMapResult).String()
	if !strings.Contains(out, `"path":"main_test.go"`) || !strings.Contains(out, `"name":"TestMain"`) {
		t.Fatalf("json output missing test declaration: %s", out)
	}
}

func writeFileMapFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}
