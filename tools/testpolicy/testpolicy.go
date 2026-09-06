// Package testpolicy enforces repository test-source policies.
package testpolicy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBase is the comparison ref used when callers do not provide one.
	DefaultBase    = "origin/main"
	commandTimeout = 30 * time.Second
	waitDelay      = 2 * time.Second
)

var (
	// ErrViolations reports that policy diagnostics were written.
	ErrViolations = errors.New("test policy violations")
	// ErrUnknownBase reports that the requested comparison ref is not a commit.
	ErrUnknownBase = errors.New("unknown test-policy base")
)

type changeKind byte

const (
	added    changeKind = 'A'
	modified changeKind = 'M'
)

type changedFile struct {
	path       string
	kind       changeKind
	addedLines map[int]string
}

// Run checks the full repository tree and additions relative to base.
// An empty base selects DefaultBase.
func Run(ctx context.Context, root, base string, stderr io.Writer) error {
	if base == "" {
		base = DefaultBase
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	if _, err := git(ctx, absRoot, "rev-parse", "--verify", base+"^{commit}"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", ErrUnknownBase, base)
	}

	changes, err := changedFiles(ctx, absRoot, base)
	if err != nil {
		return err
	}
	problems := checkChanges(absRoot, changes)
	treeProblems, err := checkTree(absRoot)
	if err != nil {
		return err
	}
	problems = append(problems, treeProblems...)
	sort.Strings(problems)
	problems = compact(problems)
	for _, problem := range problems {
		fmt.Fprintln(stderr, problem)
	}
	if len(problems) != 0 {
		return ErrViolations
	}
	return nil
}

func changedFiles(ctx context.Context, root, base string) ([]changedFile, error) {
	output, err := git(ctx, root, "diff", "--name-status", "-z", "--diff-filter=AM", base, "--", "*_test.go")
	if err != nil {
		return nil, fmt.Errorf("list changed tests: %w", err)
	}
	changes := make(map[string]changeKind)
	fields := bytes.Split(output, []byte{0})
	for index := 0; index+1 < len(fields); index += 2 {
		status := string(fields[index])
		path := string(fields[index+1])
		if status == "A" || status == "M" {
			changes[path] = changeKind(status[0])
		}
	}
	output, err = git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z", "--", "*_test.go")
	if err != nil {
		return nil, fmt.Errorf("list untracked tests: %w", err)
	}
	for _, field := range bytes.Split(output, []byte{0}) {
		if len(field) != 0 {
			changes[string(field)] = added
		}
	}

	paths := make([]string, 0, len(changes))
	for path := range changes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]changedFile, 0, len(paths))
	for _, path := range paths {
		lines, err := addedLines(ctx, root, base, path, changes[path] == added && !trackedAtBase(ctx, root, base, path))
		if err != nil {
			return nil, fmt.Errorf("read additions for %s: %w", path, err)
		}
		result = append(result, changedFile{path: path, kind: changes[path], addedLines: lines})
	}
	return result, nil
}

func trackedAtBase(ctx context.Context, root, base, path string) bool {
	_, err := git(ctx, root, "cat-file", "-e", base+":"+path)
	return err == nil
}

func addedLines(ctx context.Context, root, base, path string, untracked bool) (map[int]string, error) {
	if untracked {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("read file: %w", err)
		}
		return allLines(body), nil
	}
	output, err := git(ctx, root, "diff", "--no-ext-diff", "--no-color", "--unified=0", base, "--", path)
	if err != nil {
		return nil, err
	}
	return parseAddedLines(output), nil
}

func allLines(body []byte) map[int]string {
	lines := make(map[int]string)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for line := 1; scanner.Scan(); line++ {
		lines[line] = scanner.Text()
	}
	return lines
}

func parseAddedLines(diff []byte) map[int]string {
	lines := make(map[int]string)
	newLine := 0
	inHunk := false
	hunk := regexp.MustCompile(`^@@ -[^ ]+ \+(\d+)`)
	scanner := bufio.NewScanner(bytes.NewReader(diff))
	for scanner.Scan() {
		text := scanner.Text()
		if match := hunk.FindStringSubmatch(text); match != nil {
			newLine, _ = strconv.Atoi(match[1])
			inHunk = true
			continue
		}
		if !inHunk || strings.HasPrefix(text, "+++") {
			continue
		}
		switch {
		case strings.HasPrefix(text, "+"):
			lines[newLine] = strings.TrimPrefix(text, "+")
			newLine++
		case strings.HasPrefix(text, " "):
			newLine++
		}
	}
	return lines
}

func checkChanges(root string, changes []changedFile) []string {
	var problems []string
	entryPoint := regexp.MustCompile(`^func (Test|Benchmark|Example)[A-Za-z0-9_]*\(`)
	for _, change := range changes {
		path := filepath.Join(root, filepath.FromSlash(change.path))
		pkg, err := packageName(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: read package: %v", change.path, err))
			continue
		}
		if change.kind == added && pkg != "" && !strings.HasSuffix(pkg, "_test") {
			problems = append(problems, change.path+": new tests must use an external *_test package")
		}
		if change.kind == modified && pkg != "" && !strings.HasSuffix(pkg, "_test") {
			grandfathered := false
			newEntry := false
			for _, line := range change.addedLines {
				grandfathered = grandfathered || regexp.MustCompile(`^// ?testpolicy: grandfathered`).MatchString(line)
				newEntry = newEntry || entryPoint.MatchString(line)
			}
			if newEntry && !grandfathered {
				problems = append(problems, change.path+": new tests must use an external *_test package")
			}
		}
		for line, text := range change.addedLines {
			if strings.Contains(text, "context.Background()") && !backgroundException(change.path) {
				problems = append(problems, fmt.Sprintf("%s:%d: use t.Context() or b.Context() instead of context.Background()", change.path, line))
			}
		}
		problems = append(problems, inlineTestContexts(path, change.path, change.addedLines)...)
	}
	return problems
}

func inlineTestContexts(path, relative string, addedLines map[int]string) []string {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return []string{fmt.Sprintf("%s: parse test source: %v", relative, err)}
	}
	var problems []string
	ast.Inspect(file, func(node ast.Node) bool {
		goStatement, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		ast.Inspect(goStatement.Call, func(child ast.Node) bool {
			call, ok := child.(*ast.CallExpr)
			if !ok || !isTestContext(call.Fun) {
				return true
			}
			line := files.Position(call.Pos()).Line
			if _, added := addedLines[line]; added {
				problems = append(problems, fmt.Sprintf("%s:%d: capture t.Context() before starting a goroutine", relative, line))
			}
			return true
		})
		return false
	})
	return problems
}

func isTestContext(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "t"
}

func backgroundException(path string) bool {
	return strings.HasPrefix(path, "examples/") || filepath.Base(path) == "example_test.go"
}

func checkTree(root string) ([]string, error) {
	var problems []string
	for _, ownedRoot := range []string{"tools", "examples", "zkit", "zarlcode", "swebench-eval"} {
		dir := filepath.Join(root, ownedRoot)
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if strings.HasSuffix(entry.Name(), "_internal_test.go") {
				problems = append(problems, relative+": owned *_internal_test.go files are forbidden")
			}
			if treeException(relative) {
				return nil
			}
			pkg, err := packageName(path)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: read package: %v", relative, err))
				return nil
			}
			if pkg != "" && !strings.HasSuffix(pkg, "_test") {
				problems = append(problems, relative+": owned tests must use an external *_test package")
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("walk %s: %w", ownedRoot, err)
		}
	}
	return problems, nil
}

func packageName(path string) (string, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, nil, parser.PackageClauseOnly)
	if err != nil {
		return "", err
	}
	return file.Name.Name, nil
}

func treeException(path string) bool {
	return strings.HasPrefix(path, "zarlcode/docs/images/workflow-demo-fixture/") || path == "zarlcode/tui/behavior_surface_export_test.go"
}

func compact(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", args...)
	cmd.Dir = root
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if commandCtx.Err() != nil {
			return nil, commandCtx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, detail)
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}
