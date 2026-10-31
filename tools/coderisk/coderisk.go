// Package coderisk joins Go statement coverage with function complexity to
// produce a report-only change-risk baseline.
package coderisk

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fzipp/gocyclo"
	"golang.org/x/tools/cover"
)

// Function describes a named function or method, including nested closures.
// Coverage is a percentage; Coverage and CRAP are nil when no executable
// statements were measured. Missing data is never treated as zero coverage.
type Function struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Column     int      `json:"column"`
	Package    string   `json:"package"`
	Name       string   `json:"name"`
	Complexity int      `json:"complexity"`
	Statements int      `json:"statements"`
	Covered    int      `json:"covered"`
	Coverage   *float64 `json:"coverage_percent"`
	CRAP       *float64 `json:"crap"`
}

type listedPackage struct {
	Dir        string   `json:"Dir"`
	ImportPath string   `json:"ImportPath"`
	GoFiles    []string `json:"GoFiles"`
	CgoFiles   []string `json:"CgoFiles"`
}

// Analyze reads a go list -json stream and its matching go test -coverprofile
// from the same checkout and build configuration. Only listed production files
// are scored; generated files and bodyless declarations are excluded. Source
// paths in the result are relative to root. Profiles use import-qualified or
// absolute filenames, as emitted by Go. Anonymous functions are attributed to
// their enclosing named function; package-level function literals are omitted.
func Analyze(root string, packages, coverage io.Reader) ([]Function, error) {
	profiles, err := readProfiles(coverage)
	if err != nil {
		return nil, err
	}
	var functions []Function
	decoder := json.NewDecoder(packages)
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode package inventory: %w", err)
		}
		if pkg.Dir == "" || pkg.ImportPath == "" {
			return nil, errors.New("package inventory requires Dir and ImportPath")
		}
		for _, name := range append(pkg.GoFiles, pkg.CgoFiles...) {
			filename := filepath.Join(pkg.Dir, name)
			profile, ok := profiles[path.Join(pkg.ImportPath, filepath.ToSlash(name))]
			if !ok {
				profile = profiles[filepath.ToSlash(filename)]
			}
			found, err := analyzeFile(root, filename, pkg.ImportPath, profile)
			if err != nil {
				return nil, err
			}
			functions = append(functions, found...)
		}
	}
	if len(functions) == 0 {
		return nil, errors.New("package inventory contains no scoreable functions")
	}
	return functions, nil
}

func readProfiles(input io.Reader) (map[string]cover.Profile, error) {
	reader := bufio.NewReader(input)
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read coverage header: %w", err)
	}
	switch strings.TrimSpace(header) {
	case "mode: set", "mode: count", "mode: atomic":
	default:
		return nil, fmt.Errorf("invalid coverage header %q", strings.TrimSpace(header))
	}
	parsed, err := cover.ParseProfilesFromReader(io.MultiReader(strings.NewReader(header), reader))
	if err != nil {
		return nil, fmt.Errorf("parse coverage: %w", err)
	}
	profiles := make(map[string]cover.Profile, len(parsed))
	for _, profile := range parsed {
		profiles[filepath.ToSlash(profile.FileName)] = *profile
	}
	return profiles, nil
}

func analyzeFile(root, filename, pkg string, profile cover.Profile) ([]Function, error) {
	if strings.HasSuffix(filename, "_test.go") {
		return nil, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse source %s: %w", filename, err)
	}
	if ast.IsGenerated(file) {
		return nil, nil
	}
	relative, err := filepath.Rel(root, filename)
	if err != nil {
		return nil, fmt.Errorf("relativize source: %w", err)
	}
	var functions []Function
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name, err := functionName(fset, fn)
		if err != nil {
			return nil, err
		}
		// Ignore //line directives: coverage coordinates refer to the real file.
		start := fset.PositionFor(fn.Pos(), false)
		end := fset.PositionFor(fn.End(), false)
		result := Function{
			File: filepath.ToSlash(relative), Line: start.Line, Column: start.Column,
			Package: pkg, Name: name, Complexity: gocyclo.Complexity(fn),
		}
		for _, block := range profile.Blocks {
			if before(block.StartLine, block.StartCol, start) || !before(block.StartLine, block.StartCol, end) {
				continue
			}
			result.Statements += block.NumStmt
			if block.Count > 0 {
				result.Covered += block.NumStmt
			}
		}
		if result.Statements > 0 {
			fraction := float64(result.Covered) / float64(result.Statements)
			coverage := 100 * fraction
			cc := float64(result.Complexity)
			uncovered := 1 - fraction
			risk := cc*cc*uncovered*uncovered*uncovered + cc
			result.Coverage, result.CRAP = &coverage, &risk
		}
		functions = append(functions, result)
	}
	return functions, nil
}

func before(line, column int, position token.Position) bool {
	return line < position.Line || line == position.Line && column < position.Column
}

func functionName(fset *token.FileSet, fn *ast.FuncDecl) (string, error) {
	if fn.Recv == nil {
		return fn.Name.Name, nil
	}
	var receiver bytes.Buffer
	if err := format.Node(&receiver, fset, fn.Recv.List[0].Type); err != nil {
		return "", fmt.Errorf("format receiver: %w", err)
	}
	return "(" + receiver.String() + ")." + fn.Name.Name, nil
}

// Rank sorts functions in place by descending CRAP, with unmeasured functions
// last. Complexity and source location break ties deterministically.
func Rank(functions []Function) {
	slices.SortFunc(functions, func(a, b Function) int {
		if a.CRAP == nil && b.CRAP != nil {
			return 1
		}
		if a.CRAP != nil && b.CRAP == nil {
			return -1
		}
		if a.CRAP != nil && b.CRAP != nil {
			if order := cmp.Compare(*b.CRAP, *a.CRAP); order != 0 {
				return order
			}
		}
		return cmp.Or(cmp.Compare(b.Complexity, a.Complexity),
			cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
	})
}
