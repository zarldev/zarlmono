package coderunner_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/agent/coderunner"
)

func TestCommandProbeRejectsMissingInputs(t *testing.T) {
	t.Parallel()
	if coderunner.CommandProbe(t.TempDir(), nil, []string{"true"}, coderunner.ProbeOpts{})(t.Context()) {
		t.Fatal("nil diff function reported solved")
	}
	if coderunner.CommandProbe(t.TempDir(), func() string { return "changed" }, nil, coderunner.ProbeOpts{})(t.Context()) {
		t.Fatal("empty command reported solved")
	}
}

func TestCommandProbeRunsOnlyForChangedDiff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	countPath := filepath.Join(root, "count")
	diff := "v1"
	probe := coderunner.CommandProbe(root, func() string { return diff }, []string{
		"sh", "-c", "n=0; test ! -f count || n=$(cat count); echo $((n+1)) > count; false",
	}, coderunner.ProbeOpts{})

	if probe(t.Context()) {
		t.Fatal("first failing command reported solved")
	}
	if probe(t.Context()) {
		t.Fatal("unchanged failing command reported solved")
	}
	assertCount(t, countPath, 1)
	diff = "v2"
	if probe(t.Context()) {
		t.Fatal("failing command reported solved after diff change")
	}
	assertCount(t, countPath, 2)
}

func TestCommandProbeFailsClosedThenPasses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	diff := "v1"
	probe := coderunner.CommandProbe(root, func() string { return diff }, []string{"sh", "-c", "test -f solved"}, coderunner.ProbeOpts{})
	if probe(t.Context()) {
		t.Fatal("missing solved marker reported solved")
	}
	if err := os.WriteFile(filepath.Join(root, "solved"), []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	diff = "v2"
	if !probe(t.Context()) {
		t.Fatal("successful command did not report solved")
	}
}

func TestCommandProbeHonorsRunBounds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	countPath := filepath.Join(root, "count")
	n := 0
	probe := coderunner.CommandProbe(root, func() string {
		n++
		return strconv.Itoa(n)
	}, []string{"sh", "-c", "n=0; test ! -f count || n=$(cat count); echo $((n+1)) > count; false"}, coderunner.ProbeOpts{
		MaxRuns:     2,
		MinInterval: time.Hour,
	})

	probe(t.Context())
	probe(t.Context())
	assertCount(t, countPath, 1)
}

func TestCommandProbeHonorsMaxRuns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	countPath := filepath.Join(root, "count")
	n := 0
	probe := coderunner.CommandProbe(root, func() string {
		n++
		return strconv.Itoa(n)
	}, []string{"sh", "-c", "n=0; test ! -f count || n=$(cat count); echo $((n+1)) > count; false"}, coderunner.ProbeOpts{MaxRuns: 2})
	for range 5 {
		probe(t.Context())
	}
	assertCount(t, countPath, 2)
}

func assertCount(t *testing.T, path string, want int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("command runs = %d, want %d", got, want)
	}
}
