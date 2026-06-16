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

// browserActionTimeout bounds a single chromedp action. Without it, a
// broken selector or hung page makes chromedp wait until the runner's
// tool timeout — a multi-minute silent stall. With it, a bad page
// fails in seconds with a clear error.
const browserActionTimeout = 20 * time.Second

// browserSettleWait is the time chromedp waits after the page reaches
// "complete" readyState before extracting text. Gives JS-rendered
// content (React/Vue/Svelte hydration) time to appear.
const browserSettleWait = 1500 * time.Millisecond

// fetchWithBrowser launches headless Chrome via chromedp, navigates to
// rawURL, waits for the page to settle, and returns the page title and
// visible text content (or targeted selector text when sel is set).
// chromeBinPath is an optional absolute path to a Chrome/Chromium binary;
// empty means chromedp searches standard platform paths.
func fetchWithBrowser(ctx context.Context, rawURL, sel string, maxChars int, chromeBinPath string) (string, string, error) {
	resolvedChrome, err := resolveChromeBinary(chromeBinPath)
	if err != nil {
		return "", "", err
	}
	scratch, err := newChromeScratchDirs()
	if err != nil {
		return "", "", fmt.Errorf("prepare chrome scratch dirs: %w", err)
	}

	var chromeOut syncBuffer

	// Create a browser context separate from the caller's so the
	// action timeout doesn't race with the runner's tool timeout.
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
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
		chromedp.CombinedOutput(&chromeOut),
	)
	opts = append(opts, chromedp.ExecPath(resolvedChrome))
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer func() {
		allocCancel()
		_ = os.RemoveAll(scratch.root)
	}()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	// Warm the browser — a missing Chrome binary fails here, not mid-action.
	if err := chromedp.Run(browserCtx); err != nil {
		return "", "", chromeFailure("start chrome", resolvedChrome, chromeOut.String(), err)
	}

	// Navigate and wait for the page to settle.
	actx, actCancel := context.WithTimeout(browserCtx, browserActionTimeout)
	defer actCancel()
	stop := context.AfterFunc(ctx, actCancel)
	defer stop()

	if err := chromedp.Run(actx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body"),
		chromedp.Sleep(browserSettleWait),
	); err != nil {
		return "", "", chromeFailure("navigate", resolvedChrome, chromeOut.String(), err)
	}

	// Extract title.
	var title string
	actx2, actCancel2 := context.WithTimeout(browserCtx, browserActionTimeout)
	defer actCancel2()
	stop2 := context.AfterFunc(ctx, actCancel2)
	defer stop2()

	if err := chromedp.Run(actx2,
		chromedp.Title(&title),
	); err != nil {
		// Title failure is non-fatal — return what we can.
		title = ""
	}

	// Extract body text.
	actx3, actCancel3 := context.WithTimeout(browserCtx, browserActionTimeout)
	defer actCancel3()
	stop3 := context.AfterFunc(ctx, actCancel3)
	defer stop3()

	var body string
	if sel != "" {
		// Targeted extraction: get the text of the selected element.
		var elText string
		if err := chromedp.Run(actx3,
			chromedp.Text(sel, &elText, chromedp.ByQuery),
		); err != nil {
			return title, "", chromeFailure(fmt.Sprintf("extract selector %q", sel), resolvedChrome, chromeOut.String(), err)
		}
		body = strings.TrimSpace(elText)
	} else {
		// Full page: get document.body.innerText.
		var pageText string
		if err := chromedp.Run(actx3,
			chromedp.Evaluate(`document.body ? document.body.innerText : document.documentElement.innerText`, &pageText),
		); err != nil {
			return title, "", chromeFailure("extract body text", resolvedChrome, chromeOut.String(), err)
		}
		body = strings.TrimSpace(pageText)
	}

	body = collapseWS(body)
	if len(body) > maxChars {
		body = body[:maxChars]
		if lastDot := strings.LastIndex(body, ". "); lastDot > maxChars/2 {
			body = body[:lastDot+1]
		}
		body += "\n\n[truncated]"
	}
	return title, body, nil
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
