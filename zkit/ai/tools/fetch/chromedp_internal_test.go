package fetch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChromeProfileBaseDirPrefersUserCache verifies the browser profile dir
// stays under the user cache root rather than using chromedp's default
// os.TempDir-based scratch directory.
func TestChromeProfileBaseDirPrefersUserCache(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "cache-root")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	dir, err := chromeProfileBaseDir()
	if err != nil {
		t.Fatalf("chromeProfileBaseDir: %v", err)
	}
	want := filepath.Join(cacheRoot, "zarlcode", "chromedp")
	if dir != want {
		t.Fatalf("profile dir: got %q, want %q", dir, want)
	}
}

// TestNewChromeScratchDirsKeepsAllPathsUnderRoot verifies every Chrome scratch
// path we control is redirected under a single user-writable root.
func TestNewChromeScratchDirsKeepsAllPathsUnderRoot(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "cache-root")
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	scratch, err := newChromeScratchDirs()
	if err != nil {
		t.Fatalf("newChromeScratchDirs: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch.root) })

	for _, dir := range []string{
		scratch.profile,
		scratch.dataPath,
		scratch.diskCache,
		scratch.crashDumps,
		scratch.tmpDir,
		scratch.xdgCache,
		scratch.xdgConfig,
	} {
		if !filepath.IsAbs(dir) {
			t.Fatalf("path %q is not absolute", dir)
		}
		rel, err := filepath.Rel(scratch.root, dir)
		if err != nil {
			t.Fatalf("filepath.Rel(%q, %q): %v", scratch.root, dir, err)
		}
		if rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator) {
			t.Fatalf("path %q escapes scratch root %q", dir, scratch.root)
		}
	}
}

// TestResolveChromeBinaryMissing gives a clearer error when no browser is
// installed instead of deferring to a generic chromedp startup failure.
// The candidate list is bogus rather than emptying PATH: real candidates
// include absolute paths (e.g. /snap/bin/chromium) that exec.LookPath
// resolves directly, so a host with any browser installed — including CI
// runners — would otherwise find one and the test would be non-hermetic.
func TestResolveChromeBinaryMissing(t *testing.T) {
	_, err := resolveChromeBinaryFrom("", []string{"zarlmono-no-such-browser-binary"})
	if err == nil {
		t.Fatal("resolveChromeBinaryFrom: expected error, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "no Chrome/Chromium browser binary found") {
		t.Fatalf("resolveChromeBinaryFrom error %q does not mention missing browser", got)
	}
}

func TestDiagnoseChromeLaunchFailure_PermissionDenied(t *testing.T) {
	hint := diagnoseChromeLaunchFailure(stringError("permission denied"))
	if !strings.Contains(hint, "launch was denied") {
		t.Fatalf("diagnoseChromeLaunchFailure hint %q does not mention denied launch", hint)
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

func TestSummarizeChromeOutput(t *testing.T) {
	out := "first line\nsecond line\nthird line"
	got := summarizeChromeOutput(out)
	if !strings.Contains(got, "first line") || !strings.Contains(got, "third line") {
		t.Fatalf("summarizeChromeOutput(%q) = %q", out, got)
	}
}
