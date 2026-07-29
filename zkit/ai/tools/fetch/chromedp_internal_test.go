package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestRendererOptions(t *testing.T) {
	cfg := rendererConfig{
		concurrency: defaultRenderConcurrency,
		settleWait:  browserSettleWait,
		actionLimit: browserActionTimeout,
	}
	for _, opt := range []rendererOption{
		withChromePath("/chrome"),
		withRenderConcurrency(3),
		withSettleWait(time.Second),
		withActionTimeout(2 * time.Second),
	} {
		opt(&cfg)
	}
	if cfg.chromePath != "/chrome" || cfg.concurrency != 3 || cfg.settleWait != time.Second || cfg.actionLimit != 2*time.Second {
		t.Fatalf("renderer options produced unexpected config: %+v", cfg)
	}
}

func TestRendererSlotCancellation(t *testing.T) {
	r := &renderer{slots: make(chan struct{}, 1)}
	r.slots <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := r.render(ctx, renderRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("render error = %v, want context.Canceled", err)
	}
}

func TestRendererCloseIdempotent(t *testing.T) {
	root := t.TempDir()
	r := &renderer{scratch: chromeScratchDirs{root: root}}
	if err := r.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch root stat error = %v, want not exist", err)
	}
	_, err := r.render(t.Context(), renderRequest{})
	if !errors.Is(err, errRendererClosed) {
		t.Fatalf("render after Close error = %v, want errRendererClosed", err)
	}
}

func TestRendererIntegration(t *testing.T) {
	chromePath, err := resolveChromeBinary("")
	if err != nil {
		t.Skipf("Chrome unavailable: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch req.URL.Path {
		case "/redirect":
			http.Redirect(w, req, "/one", http.StatusFound)
		case "/one":
			_, _ = w.Write([]byte(`<html><head><title>One</title></head><body><main>first page</main></body></html>`))
		case "/two":
			_, _ = w.Write([]byte(`<html><head><title>Two</title></head><body><main>second page</main><script>setTimeout(() => document.querySelector('main').textContent = 'hydrated page', 50)</script></body></html>`))
		case "/slow":
			_, _ = w.Write([]byte(`<html><body><script>setTimeout(() => document.body.textContent = 'late', 2000)</script></body></html>`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	r, err := newRenderer(t.Context(),
		withChromePath(chromePath),
		withRenderConcurrency(1),
		withSettleWait(100*time.Millisecond),
		withActionTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}
	root := r.scratch.root
	t.Cleanup(func() { _ = r.Close() })

	first, err := r.render(t.Context(), renderRequest{URL: server.URL + "/redirect", MaxChars: 1000})
	if err != nil {
		t.Fatalf("render first page: %v", err)
	}
	if first.Title != "One" || !strings.Contains(first.Text, "first page") || first.URL != server.URL+"/one" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := r.render(t.Context(), renderRequest{URL: server.URL + "/two", Selector: "main", MaxChars: 1000})
	if err != nil {
		t.Fatalf("render second page: %v", err)
	}
	if second.Title != "Two" || second.Text != "hydrated page" {
		t.Fatalf("second page = %+v", second)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = r.render(ctx, renderRequest{URL: server.URL + "/slow", MaxChars: 1000, SettleWait: time.Second})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled render error = %v, want deadline exceeded", err)
	}

	const concurrent = 4
	var wg sync.WaitGroup
	errCh := make(chan error, concurrent)
	for range concurrent {
		wg.Go(func() {
			_, renderErr := r.render(t.Context(), renderRequest{URL: server.URL + "/one", MaxChars: 1000})
			errCh <- renderErr
		})
	}
	wg.Wait()
	close(errCh)
	for renderErr := range errCh {
		if renderErr != nil {
			t.Errorf("concurrent render: %v", renderErr)
		}
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch root stat error = %v, want not exist", err)
	}
}
