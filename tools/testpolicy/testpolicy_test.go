package testpolicy_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/tools/testpolicy"
)

func TestRunAcceptsExternalTestsAndTreeExceptions(t *testing.T) {
	root := gitFixture(t)
	writeFile(t, root, "zkit/widget/widget_test.go", "package widget_test\n")
	writeFile(t, root, "zarlcode/docs/images/workflow-demo-fixture/demo_test.go", "package demo\n")
	writeFile(t, root, "zarlcode/tui/behavior_surface_export_test.go", "package tui\n")
	commitAll(t, root)
	var stderr bytes.Buffer

	err := testpolicy.Run(t.Context(), root, "HEAD", &stderr)

	if err != nil {
		t.Fatalf("Run() error = %v; stderr = %q", err, stderr.String())
	}
}

func TestRunReportsAddedLinePolicies(t *testing.T) {
	root := gitFixture(t)
	writeFile(t, root, "zkit/widget/widget_test.go", "package widget_test\n\nimport \"testing\"\n\nfunc TestExisting(t *testing.T) {}\n")
	commitAll(t, root)
	body := `package widget_test

import (
	"context"
	"testing"
)

func TestExisting(t *testing.T) {}

func TestAdded(t *testing.T) {
	_ = BACKGROUND
	go func() {
		_ = t.Context()
	}()
	go use(t.Context())
}

func use(context.Context) {}
`
	body = strings.ReplaceAll(body, "BACKGROUND", "context."+"Background()")
	writeFile(t, root, "zkit/widget/widget_test.go", body)
	var stderr bytes.Buffer

	err := testpolicy.Run(t.Context(), root, "HEAD", &stderr)

	if !errors.Is(err, testpolicy.ErrViolations) {
		t.Fatalf("Run() error = %v, want ErrViolations", err)
	}
	for _, diagnostic := range []string{
		"zkit/widget/widget_test.go:11: use t.Context() or b.Context()",
		"zkit/widget/widget_test.go:13: capture t.Context() before starting a goroutine",
		"zkit/widget/widget_test.go:15: capture t.Context() before starting a goroutine",
	} {
		if !strings.Contains(stderr.String(), diagnostic) {
			t.Errorf("stderr %q does not contain %q", stderr.String(), diagnostic)
		}
	}
}

func TestRunIncludesUntrackedTestsAndFullTreePolicy(t *testing.T) {
	root := gitFixture(t)
	writeFile(t, root, "README.md", "fixture\n")
	commitAll(t, root)
	writeFile(t, root, "tools/sample/new_test.go", "package sample\n\nfunc TestNew() {}\n")
	writeFile(t, root, "examples/sample/bad_internal_test.go", "package sample_test\n")
	var stderr bytes.Buffer

	err := testpolicy.Run(t.Context(), root, "HEAD", &stderr)

	if !errors.Is(err, testpolicy.ErrViolations) {
		t.Fatalf("Run() error = %v, want ErrViolations", err)
	}
	for _, diagnostic := range []string{
		"tools/sample/new_test.go: new tests must use an external *_test package",
		"tools/sample/new_test.go: owned tests must use an external *_test package",
		"examples/sample/bad_internal_test.go: owned *_internal_test.go files are forbidden",
	} {
		if !strings.Contains(stderr.String(), diagnostic) {
			t.Errorf("stderr %q does not contain %q", stderr.String(), diagnostic)
		}
	}
}

func gitFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "--quiet")
	return root
}

func commitAll(t *testing.T, root string) {
	t.Helper()
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "fixture")
}

func git(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func writeFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
