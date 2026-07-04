// Package sourcecode parses source files into deterministic structural metadata.
package sourcecode

// Language identifies the parser/language used for a source file.
type Language string

// Supported source languages.
const (
	LanguageGo Language = "go"
)

// SymbolKind identifies the structural kind of a parsed symbol.
type SymbolKind string

// Supported symbol kinds.
const (
	SymbolKindConst     SymbolKind = "const"
	SymbolKindVar       SymbolKind = "var"
	SymbolKindType      SymbolKind = "type"
	SymbolKindTypeAlias SymbolKind = "type_alias"
	SymbolKindStruct    SymbolKind = "struct"
	SymbolKindInterface SymbolKind = "interface"
	SymbolKindFunc      SymbolKind = "func"
	SymbolKindMethod    SymbolKind = "method"
)

// IsType reports whether k is any named type declaration shape.
func (k SymbolKind) IsType() bool {
	switch k {
	case SymbolKindType, SymbolKindTypeAlias, SymbolKindStruct, SymbolKindInterface:
		return true
	default:
		return false
	}
}

// File is a parsed source file structure suitable for maps, retrieval, and chunking.
type File struct {
	Path      string
	Language  Language
	Package   string
	Imports   []Import
	Symbols   []Symbol
	Generated bool
}

// Import describes one source import/dependency declaration.
type Import struct {
	Name string
	Path string
}

// Symbol is a deterministic top-level source declaration range.
type Symbol struct {
	Kind        SymbolKind
	Name        string
	Receiver    string
	Signature   string
	StartLine   int
	EndLine     int
	StartOffset int
	EndOffset   int
}

// Parser parses a source file into structural metadata.
type Parser interface {
	Parse(path string, src []byte) (File, error)
}
