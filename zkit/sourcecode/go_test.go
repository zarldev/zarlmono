package sourcecode_test

import (
	"testing"

	"github.com/zarldev/zarlmono/zkit/sourcecode"
)

func TestGoParserParsesImportsAndSymbols(t *testing.T) {
	src := []byte(`package demo

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
	file, err := (sourcecode.GoParser{}).Parse("demo.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if file.Package != "demo" || file.Language != "go" {
		t.Fatalf("file metadata = %#v", file)
	}
	if len(file.Imports) != 2 || file.Imports[1].Name != "alias" || file.Imports[1].Path != `"io"` {
		t.Fatalf("imports = %#v", file.Imports)
	}
	want := []struct {
		kind       sourcecode.SymbolKind
		name, recv string
	}{
		{sourcecode.SymbolKindConst, "version", ""},
		{sourcecode.SymbolKindVar, "count", ""},
		{sourcecode.SymbolKindStruct, "Thing", ""},
		{sourcecode.SymbolKindInterface, "Reader", ""},
		{sourcecode.SymbolKindFunc, "NewThing", ""},
		{sourcecode.SymbolKindMethod, "Print", "*Thing"},
	}
	if len(file.Symbols) != len(want) {
		t.Fatalf("symbols = %#v", file.Symbols)
	}
	for i, w := range want {
		got := file.Symbols[i]
		if got.Kind != w.kind || got.Name != w.name || got.Receiver != w.recv {
			t.Fatalf("symbol %d = %#v, want %#v", i, got, w)
		}
		if got.StartLine < 1 || got.EndLine < got.StartLine || got.StartOffset >= got.EndOffset {
			t.Fatalf("symbol %d has bad range: %#v", i, got)
		}
	}
}
