package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/zarldev/zarlmono/zkit/filesystem"
	"github.com/zarldev/zarlmono/zkit/options"
)

// syncBuffer is a bytes.Buffer guarded by a mutex. chromedp's
// CombinedOutput copies the browser process's stdout/stderr into the
// supplied writer from a background goroutine that outlives a failed
// chromedp.Run — so reading the buffer in the launch/navigate failure
// paths (chromeOut.String()) races that goroutine's writes. The lock
// serialises the two; everything else about it is a plain bytes.Buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p under the lock, satisfying io.Writer for chromedp's
// CombinedOutput sink.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns the captured output under the lock, safe to call while
// chromedp's copy goroutine is still writing.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type chromeScratchDirs struct {
	root       string
	profile    string
	dataPath   string
	diskCache  string
	crashDumps string
	tmpDir     string
	xdgCache   string
	xdgConfig  string
}

// browserActionTimeout bounds all actions for one page render.
const browserActionTimeout = 20 * time.Second

// browserSettleWait gives client-side frameworks time to hydrate after body readiness.
const browserSettleWait = 1500 * time.Millisecond

const defaultRenderConcurrency = 2

var errRendererClosed = errors.New("renderer closed")

type rendererConfig struct {
	chromePath  string
	concurrency int
	settleWait  time.Duration
	actionLimit time.Duration
	newScratch  func() (chromeScratchDirs, error)
}

type rendererOption = options.Option[rendererConfig]

func withChromePath(path string) rendererOption {
	return func(cfg *rendererConfig) { cfg.chromePath = path }
}

func withRenderConcurrency(n int) rendererOption {
	return func(cfg *rendererConfig) { cfg.concurrency = n }
}

func withSettleWait(wait time.Duration) rendererOption {
	return func(cfg *rendererConfig) { cfg.settleWait = wait }
}

func withActionTimeout(timeout time.Duration) rendererOption {
	return func(cfg *rendererConfig) { cfg.actionLimit = timeout }
}

type renderer struct {
	allocCancel   context.CancelFunc
	browserCtx    context.Context
	browserCancel context.CancelFunc
	scratch       chromeScratchDirs
	chromePath    string
	chromeOut     syncBuffer
	slots         chan struct{}
	settleWait    time.Duration
	actionLimit   time.Duration

	mu        sync.Mutex
	closed    bool
	active    sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

type renderRequest struct {
	URL        string
	Selector   string
	MaxChars   int
	SettleWait time.Duration
}

type renderedPage struct {
	URL   string
	Title string
	Text  string
}

func newRenderer(ctx context.Context, opts ...rendererOption) (*renderer, error) {
	cfg := rendererConfig{
		concurrency: defaultRenderConcurrency,
		settleWait:  browserSettleWait,
		actionLimit: browserActionTimeout,
		newScratch:  newChromeScratchDirs,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.concurrency <= 0 {
		return nil, fmt.Errorf("render concurrency must be positive: %d", cfg.concurrency)
	}
	if cfg.settleWait < 0 {
		return nil, fmt.Errorf("settle wait must not be negative: %s", cfg.settleWait)
	}
	if cfg.actionLimit <= 0 {
		return nil, fmt.Errorf("action timeout must be positive: %s", cfg.actionLimit)
	}

	resolvedChrome, err := resolveChromeBinary(cfg.chromePath)
	if err != nil {
		return nil, err
	}
	scratch, err := cfg.newScratch()
	if err != nil {
		return nil, fmt.Errorf("prepare chrome scratch dirs: %w", err)
	}

	r := &renderer{
		scratch:     scratch,
		chromePath:  resolvedChrome,
		slots:       make(chan struct{}, cfg.concurrency),
		settleWait:  cfg.settleWait,
		actionLimit: cfg.actionLimit,
	}
	execOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(scratch.profile),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("headless", true),
		chromedp.Flag("data-path", scratch.dataPath),
		chromedp.Flag("disk-cache-dir", scratch.diskCache),
		chromedp.Flag("crash-dumps-dir", scratch.crashDumps),
		chromedp.WindowSize(1280, 1024),
		chromedp.UserAgent(
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "+
				"AppleWebKit/537.36 (KHTML, like Gecko) "+
				"Chrome/124.0.0.0 Safari/537.36"),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Env(
			"TMPDIR="+scratch.tmpDir,
			"XDG_CACHE_HOME="+scratch.xdgCache,
			"XDG_CONFIG_HOME="+scratch.xdgConfig,
		),
		chromedp.CombinedOutput(&r.chromeOut),
		chromedp.ExecPath(resolvedChrome),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, execOpts...)
	r.allocCancel = allocCancel
	r.browserCtx, r.browserCancel = chromedp.NewContext(allocCtx)

	if err := chromedp.Run(r.browserCtx); err != nil {
		cleanupErr := r.Close()
		return nil, errors.Join(chromeFailure("start chrome", resolvedChrome, r.chromeOut.String(), err), cleanupErr)
	}
	return r, nil
}

func (r *renderer) render(ctx context.Context, request renderRequest) (renderedPage, error) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return renderedPage{}, errRendererClosed
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return renderedPage{}, ctx.Err()
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return renderedPage{}, errRendererClosed
	}
	r.active.Add(1)
	r.mu.Unlock()
	defer r.active.Done()

	tabCtx, tabCancel := chromedp.NewContext(r.browserCtx)
	defer tabCancel()
	actionCtx, actionCancel := context.WithTimeout(tabCtx, r.actionLimit)
	defer actionCancel()
	stop := context.AfterFunc(ctx, actionCancel)
	defer stop()

	wait := r.settleWait
	if request.SettleWait > 0 {
		wait = request.SettleWait
	}
	if err := chromedp.Run(actionCtx,
		chromedp.Navigate(request.URL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(wait),
	); err != nil {
		return renderedPage{}, r.pageFailure(ctx, "navigate", request.URL, err)
	}

	page := renderedPage{}
	if err := chromedp.Run(actionCtx,
		chromedp.Location(&page.URL),
		chromedp.Title(&page.Title),
	); err != nil {
		return renderedPage{}, r.pageFailure(ctx, "extract page metadata", request.URL, err)
	}

	var err error
	if request.Selector != "" {
		err = chromedp.Run(actionCtx, chromedp.Text(request.Selector, &page.Text, chromedp.ByQuery))
		if err != nil {
			return renderedPage{}, r.pageFailure(ctx, fmt.Sprintf("extract selector %q", request.Selector), request.URL, err)
		}
	} else {
		err = chromedp.Run(actionCtx, chromedp.Evaluate(
			`document.body ? document.body.innerText : document.documentElement.innerText`, &page.Text,
		))
		if err != nil {
			return renderedPage{}, r.pageFailure(ctx, "extract body text", request.URL, err)
		}
	}
	page.Text = truncateRenderedText(page.Text, request.MaxChars)
	return page, nil
}

func (r *renderer) pageFailure(ctx context.Context, stage, rawURL string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return chromeFailure(fmt.Sprintf("%s %q", stage, rawURL), r.chromePath, r.chromeOut.String(), err)
}

func truncateRenderedText(body string, maxChars int) string {
	body = collapseWS(strings.TrimSpace(body))
	if len(body) <= maxChars {
		return body
	}
	body = body[:maxChars]
	if lastDot := strings.LastIndex(body, ". "); lastDot > maxChars/2 {
		body = body[:lastDot+1]
	}
	return body + "\n\n[truncated]"
}

func (r *renderer) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		r.active.Wait()

		var cleanupErr error
		if r.browserCtx != nil {
			cleanupErr = chromedp.Cancel(r.browserCtx)
		}
		if r.browserCancel != nil {
			r.browserCancel()
		}
		if r.allocCancel != nil {
			r.allocCancel()
		}
		if r.scratch.root != "" {
			cleanupErr = errors.Join(cleanupErr, os.RemoveAll(r.scratch.root))
		}
		r.closeErr = cleanupErr
	})
	return r.closeErr
}

// resolveChromeBinary returns the browser executable path to use for chromedp.
// An explicit configured path wins; otherwise we search the common platform
// names up front so failures are reported as a clear validation error rather
// than a generic "start chrome" exec failure later.
func resolveChromeBinary(configured string) (string, error) {
	return resolveChromeBinaryFrom(configured, chromeCandidates())
}

// resolveChromeBinaryFrom is the candidate-injectable core of
// resolveChromeBinary. Candidates may be bare names (resolved via PATH) or
// absolute paths (checked directly); exec.LookPath handles both. Taking the
// list as a parameter keeps the resolution testable without depending on
// which browsers happen to be installed on the host.
func resolveChromeBinaryFrom(configured string, candidates []string) (string, error) {
	if configured != "" {
		found, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("chrome binary not found at configured path %q: %w", configured, err)
		}
		return found, nil
	}
	for _, name := range candidates {
		if found, err := exec.LookPath(name); err == nil {
			return found, nil
		}
	}
	return "", errors.New("no Chrome/Chromium browser binary found in PATH; install chromium/google-chrome or set web_fetch chrome path in settings")
}

// Bare browser executable names searched on PATH, shared across the
// per-OS candidate lists.
const (
	binGoogleChrome    = "google-chrome"
	binChromium        = "chromium"
	binChromiumBrowser = "chromium-browser"
	binChrome          = "chrome"
)

func chromeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			binGoogleChrome,
			binChromium,
			binChromiumBrowser,
			binChrome,
		}
	case "windows":
		return []string{
			binChrome,
			"chrome.exe",
			binChromium,
			binChromiumBrowser,
			binGoogleChrome,
		}
	default:
		return []string{
			"headless_shell",
			"headless-shell",
			binChromium,
			binChromiumBrowser,
			binGoogleChrome,
			"google-chrome-stable",
			"google-chrome-beta",
			"google-chrome-unstable",
			"/usr/bin/google-chrome",
			"/usr/local/bin/chrome",
			"/snap/bin/chromium",
			binChrome,
		}
	}
}

func chromeFailure(stage, chromePath, chromeOutput string, err error) error {
	if hint := diagnoseChromeLaunchFailure(err); hint != "" {
		if tail := summarizeChromeOutput(chromeOutput); tail != "" {
			return fmt.Errorf("%s (%s): %w; %s; chrome output: %s", stage, chromePath, err, hint, tail)
		}
		return fmt.Errorf("%s (%s): %w; %s", stage, chromePath, err, hint)
	}
	if tail := summarizeChromeOutput(chromeOutput); tail != "" {
		return fmt.Errorf("%s (%s): %w; chrome output: %s", stage, chromePath, err, tail)
	}
	return fmt.Errorf("%s (%s): %w", stage, chromePath, err)
}

func diagnoseChromeLaunchFailure(err error) string {
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "permission denied"):
		return fmt.Sprintf("launch was denied by the current runtime; verify the configured browser path is executable under %s and allowed by the current sandbox/exec policy", runtimeLabel())
	case strings.Contains(msg, "exec format error"):
		return fmt.Sprintf("the resolved browser binary is not runnable by a %s process", runtimeLabel())
	default:
		return ""
	}
}

func summarizeChromeOutput(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	const maxLen = 1200
	if len(out) <= maxLen {
		return collapseWS(out)
	}
	tail := out[len(out)-maxLen:]
	if nl := strings.IndexByte(tail, '\n'); nl >= 0 && nl < len(tail)-1 {
		tail = tail[nl+1:]
	}
	return "[tail] " + collapseWS(tail)
}

// newChromeScratchDirs creates a per-run Chrome scratch tree under a
// user-writable cache root. This keeps Chrome's profile, data path, disk
// cache, crash dumps, and child-process TMPDIR off /tmp, which some sandboxes
// deny to the chromedp browser fallback.
func newChromeScratchDirs() (chromeScratchDirs, error) {
	base, err := chromeProfileBaseDir()
	if err != nil {
		return chromeScratchDirs{}, err
	}
	if err := os.MkdirAll(base, filesystem.ModePrivateDir); err != nil {
		return chromeScratchDirs{}, fmt.Errorf("mkdir %q: %w", base, err)
	}
	root, err := os.MkdirTemp(base, "run-*")
	if err != nil {
		return chromeScratchDirs{}, fmt.Errorf("mkdtemp under %q: %w", base, err)
	}
	dirs := chromeScratchDirs{
		root:       root,
		profile:    filepath.Join(root, "profile"),
		dataPath:   filepath.Join(root, "data"),
		diskCache:  filepath.Join(root, "disk-cache"),
		crashDumps: filepath.Join(root, "crash-dumps"),
		tmpDir:     filepath.Join(root, "tmp"),
		xdgCache:   filepath.Join(root, "xdg-cache"),
		xdgConfig:  filepath.Join(root, "xdg-config"),
	}
	for _, dir := range []string{dirs.profile, dirs.dataPath, dirs.diskCache, dirs.crashDumps, dirs.tmpDir, dirs.xdgCache, dirs.xdgConfig} {
		if err := os.MkdirAll(dir, filesystem.ModePrivateDir); err != nil {
			_ = os.RemoveAll(root)
			return chromeScratchDirs{}, fmt.Errorf("mkdir %q: %w", dir, err)
		}
	}
	return dirs, nil
}

// chromeProfileBaseDir returns the stable parent directory that holds
// per-request Chrome profiles for web_fetch. Prefer the user cache dir so the
// browser stays off /tmp in sandboxed environments; fall back to ~/.zarlcode
// when XDG/user-cache discovery is unavailable.
func chromeProfileBaseDir() (string, error) {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "zarlcode", "chromedp"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	if home == "" || !filepath.IsAbs(home) {
		return "", fmt.Errorf("user home %q is not an absolute path", home)
	}
	return filepath.Join(home, ".zarlcode", "cache", "chromedp"), nil
}

func runtimeLabel() string {
	if isWSL() {
		return runtime.GOOS + " (WSL)"
	}
	return runtime.GOOS
}

func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(b)), "microsoft")
}
