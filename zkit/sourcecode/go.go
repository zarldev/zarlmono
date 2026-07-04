package sourcecode

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
)

// GoParser parses Go files using the standard library parser and AST.
type GoParser struct{}

const goParseMode = parser.ParseComments | parser.SkipObjectResolution

// Parse returns deterministic structure for one Go source file.
func (GoParser) Parse(path string, src []byte) (File, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, goParseMode)
	if err != nil {
		return File{}, fmt.Errorf("parse go: %w", err)
	}
	out := File{Path: path, Language: LanguageGo, Package: f.Name.Name, Generated: ast.IsGenerated(f)}
	for _, imp := range f.Imports {
		entry := Import{Path: imp.Path.Value}
		if imp.Name != nil {
			entry.Name = imp.Name.Name
		}
		out.Imports = append(out.Imports, entry)
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if sym, ok := goFuncSymbol(fset, d); ok {
				out.Symbols = append(out.Symbols, sym)
			}
		case *ast.GenDecl:
			out.Symbols = append(out.Symbols, goGenSymbols(fset, d)...)
		}
	}
	return out, nil
}

func goFuncSymbol(fset *token.FileSet, fn *ast.FuncDecl) (Symbol, bool) {
	if fn.Body == nil {
		return Symbol{}, false
	}
	start := fset.Position(fn.Pos())
	end := fset.Position(fn.End())
	sym := Symbol{
		Kind:        SymbolKindFunc,
		Name:        fn.Name.Name,
		Signature:   goFuncSignature(fn),
		StartLine:   start.Line,
		EndLine:     end.Line,
		StartOffset: start.Offset,
		EndOffset:   end.Offset,
	}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		sym.Kind = SymbolKindMethod
		sym.Receiver = goExprString(fn.Recv.List[0].Type)
	}
	return sym, true
}

func goGenSymbols(fset *token.FileSet, d *ast.GenDecl) []Symbol {
	out := make([]Symbol, 0, len(d.Specs))
	for i, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			out = append(out, goTypeSymbol(fset, d, s, i, len(d.Specs)))
		case *ast.ValueSpec:
			kind := goValueSymbolKind(d.Tok)
			for _, name := range s.Names {
				start := fset.Position(s.Pos())
				end := fset.Position(s.End())
				out = append(out, Symbol{
					Kind:        kind,
					Name:        name.Name,
					Signature:   goValueSpecSignature(s),
					StartLine:   start.Line,
					EndLine:     end.Line,
					StartOffset: start.Offset,
					EndOffset:   end.Offset,
				})
			}
		}
	}
	return out
}

func goValueSymbolKind(tok token.Token) SymbolKind {
	switch tok {
	case token.CONST:
		return SymbolKindConst
	case token.VAR:
		return SymbolKindVar
	default:
		return SymbolKind(tok.String())
	}
}

func goTypeSymbol(fset *token.FileSet, gd *ast.GenDecl, ts *ast.TypeSpec, i, n int) Symbol {
	kind := SymbolKindType
	if ts.Assign.IsValid() {
		kind = SymbolKindTypeAlias
	} else {
		switch ts.Type.(type) {
		case *ast.StructType:
			kind = SymbolKindStruct
		case *ast.InterfaceType:
			kind = SymbolKindInterface
		}
	}
	startPos := ts.Pos()
	endPos := ts.End()
	if gd.Lparen.IsValid() {
		if i == 0 {
			startPos = gd.TokPos
		}
		if i == n-1 {
			endPos = gd.End()
		}
	} else {
		startPos = gd.TokPos
	}
	start := fset.Position(startPos)
	end := fset.Position(endPos)
	return Symbol{
		Kind:        kind,
		Name:        ts.Name.Name,
		Signature:   goExprString(ts.Type),
		StartLine:   start.Line,
		EndLine:     end.Line,
		StartOffset: start.Offset,
		EndOffset:   end.Offset,
	}
}

func goValueSpecSignature(s *ast.ValueSpec) string {
	if s.Type == nil {
		return ""
	}
	return goExprString(s.Type)
}

func goFuncSignature(fn *ast.FuncDecl) string {
	clone := *fn
	clone.Body = nil
	clone.Doc = nil
	var b bytes.Buffer
	if err := printer.Fprint(&b, token.NewFileSet(), &clone); err != nil {
		return ""
	}
	return strings.TrimSpace(b.String())
}

func goExprString(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var b bytes.Buffer
	if err := printer.Fprint(&b, token.NewFileSet(), expr); err != nil {
		return ""
	}
	return strings.TrimSpace(b.String())
}
