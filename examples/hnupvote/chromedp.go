package main

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// actionTimeout bounds a single browser action. Without it a missing or
// wrong selector makes chromedp.Click/SendKeys wait until the runner's
// (much longer) tool timeout — a multi-minute silent stall per attempt.
// With it, a bad selector fails in seconds with a labeled error the tool
// reports back, so the agent (and you) see exactly which step broke.
const actionTimeout = 20 * time.Second

// chromedpPage adapts a chromedp browser context to the Page port. It is
// the ONLY file that imports chromedp — swap it for the fake (or another
// engine) and nothing else in the package changes. The browser context
// it drives (ctx) is long-lived and shared across calls, which is how
// login state (cookies) carries from the hn_login tool to hn_upvote_top.
type chromedpPage struct{ ctx context.Context }

// run executes actions on the browser context, bounded by actionTimeout
// and cancelled if the caller's context is cancelled (the runner's
// per-tool timeout / Ctrl-C). p.ctx is the browser; callCtx is the
// runner's tool context — context.AfterFunc bridges a cancel of the
// latter into a cancel of the bounded action context.
func (p *chromedpPage) run(callCtx context.Context, actions ...chromedp.Action) error {
	actx, cancel := context.WithTimeout(p.ctx, actionTimeout)
	defer cancel()
	stop := context.AfterFunc(callCtx, cancel)
	defer stop()
	return chromedp.Run(actx, actions...)
}

func (p *chromedpPage) Goto(ctx context.Context, url string) error {
	return p.run(ctx, chromedp.Navigate(url))
}

func (p *chromedpPage) Click(ctx context.Context, sel string) error {
	return p.run(ctx, chromedp.Click(sel, chromedp.ByQuery))
}

func (p *chromedpPage) Fill(ctx context.Context, sel, val string) error {
	return p.run(ctx, chromedp.SendKeys(sel, val, chromedp.ByQuery))
}

// WaitVisible blocks until sel is present and visible — riding out any
// navigation the prior action triggered (e.g. the login POST + redirect).
// Returns an error if it doesn't appear within the bounded action timeout.
func (p *chromedpPage) WaitVisible(ctx context.Context, sel string) error {
	return p.run(ctx, chromedp.WaitVisible(sel, chromedp.ByQuery))
}

// Eval runs a JavaScript boolean expression and returns its value. Used
// to read state the visibility waits can't — e.g. an element's computed
// visibility:hidden, which WaitNotVisible misses.
func (p *chromedpPage) Eval(ctx context.Context, js string) (bool, error) {
	var b bool
	err := p.run(ctx, chromedp.Evaluate(js, &b))
	return b, err
}

// Text reads the visible text of the first node matching sel.
func (p *chromedpPage) Text(ctx context.Context, sel string) (string, error) {
	var s string
	err := p.run(ctx, chromedp.Text(sel, &s, chromedp.ByQuery))
	return s, err
}

func (p *chromedpPage) URL(ctx context.Context) (string, error) {
	var u string
	err := p.run(ctx, chromedp.Location(&u))
	return u, err
}

// newChromedp launches Chrome and returns a Page plus a cancel func the
// caller must defer. headless=false opens a visible window (useful when
// debugging the selectors against the live site).
func newChromedp(parent context.Context, headless bool) (Page, context.CancelFunc, error) {
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.Flag("headless", headless),
		// Headless Chrome otherwise has no real viewport, so inputs exist
		// in the DOM but are never "visible" — chromedp.SendKeys/Click
		// (which wait for visibility) then time out. A concrete window size
		// is the standard fix.
		chromedp.WindowSize(1280, 1024),
	)
	if headless {
		// Headless Chrome advertises "HeadlessChrome" in its User-Agent
		// and sets navigator.webdriver; HN reads those and refuses the
		// login (you land back on /login). Present as an ordinary Chrome
		// so a legitimate login to the user's own account goes through.
		// Headed Chrome already looks normal, so only headless needs this.
		opts = append(opts,
			chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
		)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parent, opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	cancel := func() { cancelCtx(); cancelAlloc() }
	// Warm the browser so a missing/broken Chrome fails here, not on the
	// first tool action mid-run.
	if err := chromedp.Run(ctx); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("start chrome: %w", err)
	}
	return &chromedpPage{ctx: ctx}, cancel, nil
}
