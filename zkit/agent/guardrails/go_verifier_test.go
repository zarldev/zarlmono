package guardrails_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/guardrails"
)

// initGoModule writes a minimal module rooted at dir. Used by tests
// that need a real `go vet`-able project on disk.
func initGoModule(t *testing.T, dir, modPath string) {
	t.Helper()
	gomod := "module " + modPath + "\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

func TestGoVerifier_PassesCleanCode(t *testing.T) {
	root := t.TempDir()
	initGoModule(t, root, "example.test/clean")
	src := `package clean

func Sum(a, b int) int { return a + b }
`
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "math.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	v := &guardrails.GoVerifier{}
	err := v.Verify(t.Context(), root, []string{"pkg/math.go"})
	if err != nil {
		t.Errorf("clean code should pass; got %v", err)
	}
}

func TestGoVerifier_FlagsVetDiagnostic(t *testing.T) {
	root := t.TempDir()
	initGoModule(t, root, "example.test/bad")
	// Printf format/argument mismatch — vet's bread and butter.
	src := `package bad

import "fmt"

func Bad() {
	fmt.Printf("%d", "not an int")
}
`
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "bad.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	v := &guardrails.GoVerifier{}
	err := v.Verify(t.Context(), root, []string{"pkg/bad.go"})
	if err == nil {
		t.Fatal("vet diagnostic should produce a non-nil error")
	}
	// vet's diagnostic mentions "Printf" — version-stable across recent Go releases.
	if !strings.Contains(err.Error(), "Printf") {
		t.Errorf("error %q does not mention Printf — vet output may have changed shape", err.Error())
	}
}

func TestGoVerifier_DedupesPackagesAcrossPaths(t *testing.T) {
	// Two files in the same package shouldn't trigger two vet invocations.
	// We can't directly observe invocation count from a black-box test, but
	// the verifier returning the same outcome regardless of how many paths
	// in the same dir are passed is the observable property.
	root := t.TempDir()
	initGoModule(t, root, "example.test/dedup")
	pkgDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.go", "b.go"} {
		src := "package pkg\n\nfunc " + strings.ToUpper(strings.TrimSuffix(f, ".go")) + "() {}\n"
		if err := os.WriteFile(filepath.Join(pkgDir, f), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	v := &guardrails.GoVerifier{}
	if err := v.Verify(t.Context(), root, []string{"pkg/a.go", "pkg/b.go"}); err != nil {
		t.Errorf("two files in same pkg: want pass, got %v", err)
	}
}

func TestGoVerifier_IgnoresPathsOutsideRoot(t *testing.T) {
	// A path that resolves outside root must not cause Verify to vet
	// something the agent shouldn't see. The verifier filters these
	// out and returns nil when no in-tree paths remain.
	root := t.TempDir()
	initGoModule(t, root, "example.test/scope")
	v := &guardrails.GoVerifier{}
	err := v.Verify(t.Context(), root, []string{"../escape.go"})
	if err != nil {
		t.Errorf("out-of-root path should be filtered, not vet'd; got %v", err)
	}
}
