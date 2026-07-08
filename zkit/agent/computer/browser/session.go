package browser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/zarldev/zarlmono/zkit/agent/computer"
	"github.com/zarldev/zarlmono/zkit/options"
)

const (
	defaultActionTimeout = 20 * time.Second
	defaultSettleWait    = 500 * time.Millisecond
	defaultWidth         = 1280
	defaultHeight        = 1024

	chromeBinGoogleChrome    = "google-chrome"
	chromeBinChromium        = "chromium"
	chromeBinChromiumBrowser = "chromium-browser"
	chromeBinChrome          = "chrome"
)

// Option configures a Session.
type Option = options.Option[Session]

// Session owns a chromedp browser context and implements computer.Observer and
// computer.Actor for browser surfaces.
type Session struct {
	ctx       context.Context
	cancel    context.CancelFunc
	allocStop context.CancelFunc

	chromePath    string
	actionTimeout time.Duration
	settleWait    time.Duration
	width         int
	height        int
	headless      bool
	scratch       *scratchDirs
	chromeOut     syncBuffer
}

var (
	_ computer.Observer = (*Session)(nil)
	_ computer.Actor    = (*Session)(nil)
)

// New creates a browser session backed by a new chromedp browser context. The
// returned session owns temporary Chrome profile/cache directories and removes
// them when Close is called.
func New(ctx context.Context, opts ...Option) (*Session, error) {
	s := &Session{
		actionTimeout: defaultActionTimeout,
		settleWait:    defaultSettleWait,
		width:         defaultWidth,
		height:        defaultHeight,
		headless:      true,
	}
	for _, opt := range opts {
		opt(s)
	}

	resolvedChrome, err := resolveChromeBinary(s.chromePath)
	if err != nil {
		return nil, err
	}
	if s.scratch == nil {
		scratch, err := newScratchDirs()
		if err != nil {
			return nil, fmt.Errorf("prepare chrome scratch dirs: %w", err)
		}
		s.scratch = scratch
	}

	allocOpts := append(defaultExecAllocatorOptions(s.headless), chromedp.UserDataDir(s.scratch.profile),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("headless", s.headless),
		chromedp.Flag("data-path", s.scratch.dataPath),
		chromedp.Flag("disk-cache-dir", s.scratch.diskCache),
		chromedp.Flag("crash-dumps-dir", s.scratch.crashDumps),
		chromedp.WindowSize(s.width, s.height),
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Env(
			"TMPDIR="+s.scratch.tmpDir,
			"XDG_CACHE_HOME="+s.scratch.xdgCache,
			"XDG_CONFIG_HOME="+s.scratch.xdgConfig,
		),
		chromedp.CombinedOutput(&s.chromeOut),
		chromedp.ExecPath(resolvedChrome),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, allocOpts...)
	s.allocStop = allocCancel
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	s.ctx = browserCtx
	s.cancel = browserCancel

	if err := chromedp.Run(browserCtx); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("start chrome at %q: %w%s", resolvedChrome, err, s.chromeFailureSuffix())
	}

	return s, nil
}

// WithChromePath configures the Chrome or Chromium executable path. When empty,
// New searches common executable names for the current platform.
func WithChromePath(path string) Option {
	return func(s *Session) {
		s.chromePath = path
	}
}

// WithHeadless configures whether Chrome runs without a visible browser window.
func WithHeadless(headless bool) Option {
	return func(s *Session) {
		s.headless = headless
	}
}

// WithActionTimeout configures the timeout for individual browser operations.
func WithActionTimeout(timeout time.Duration) Option {
	return func(s *Session) {
		if timeout > 0 {
			s.actionTimeout = timeout
		}
	}
}

// WithSettleWait configures the delay after navigation or action completion
// before observations are captured.
func WithSettleWait(wait time.Duration) Option {
	return func(s *Session) {
		if wait >= 0 {
			s.settleWait = wait
		}
	}
}

// WithWindowSize configures the browser viewport dimensions.
func WithWindowSize(width, height int) Option {
	return func(s *Session) {
		if width > 0 {
			s.width = width
		}
		if height > 0 {
			s.height = height
		}
	}
}

// Close releases the browser context and removes session scratch directories.
func (s *Session) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.allocStop != nil {
		s.allocStop()
	}
	if s.scratch != nil {
		return os.RemoveAll(s.scratch.root)
	}
	return nil
}

func (s *Session) run(ctx context.Context, actions ...chromedp.Action) error {
	actx, cancel := context.WithTimeout(s.ctx, s.actionTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return chromedp.Run(actx, actions...)
}

func (s *Session) chromeFailureSuffix() string {
	out := s.chromeOut.String()
	if out == "" {
		return ""
	}
	return ":\n" + out
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type scratchDirs struct {
	root       string
	profile    string
	dataPath   string
	diskCache  string
	crashDumps string
	tmpDir     string
	xdgCache   string
	xdgConfig  string
}

func newScratchDirs() (*scratchDirs, error) {
	root, err := os.MkdirTemp("", "zkit-computer-browser-*")
	if err != nil {
		return nil, err
	}
	dirs := &scratchDirs{
		root:       root,
		profile:    filepath.Join(root, "profile"),
		dataPath:   filepath.Join(root, "data"),
		diskCache:  filepath.Join(root, "cache"),
		crashDumps: filepath.Join(root, "crash"),
		tmpDir:     filepath.Join(root, "tmp"),
		xdgCache:   filepath.Join(root, "xdg-cache"),
		xdgConfig:  filepath.Join(root, "xdg-config"),
	}
	for _, dir := range []string{dirs.profile, dirs.dataPath, dirs.diskCache, dirs.crashDumps, dirs.tmpDir, dirs.xdgCache, dirs.xdgConfig} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
	}
	return dirs, nil
}

func resolveChromeBinary(configured string) (string, error) {
	if configured != "" {
		found, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("chrome binary not found at configured path %q: %w", configured, err)
		}
		return found, nil
	}
	for _, name := range chromeCandidates() {
		if found, err := exec.LookPath(name); err == nil {
			return found, nil
		}
	}
	return "", errors.New("no Chrome/Chromium browser binary found in PATH")
}

func chromeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			chromeBinGoogleChrome,
			chromeBinChromium,
			chromeBinChromiumBrowser,
			chromeBinChrome,
		}
	case "windows":
		return []string{chromeBinChrome, "chrome.exe", chromeBinChromium, chromeBinChromiumBrowser, chromeBinGoogleChrome}
	default:
		return []string{
			"headless_shell",
			"headless-shell",
			chromeBinChromium,
			chromeBinChromiumBrowser,
			chromeBinGoogleChrome,
			"google-chrome-stable",
			"google-chrome-beta",
			"google-chrome-unstable",
			"/usr/bin/google-chrome",
			"/usr/local/bin/chrome",
			"/snap/bin/chromium",
			chromeBinChrome,
		}
	}
}
