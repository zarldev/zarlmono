package main

import "context"

// Page is the minimal browser surface the HN tools drive. The chromedp
// adapter implements it for the real run; the test uses a fake. Keeping
// the tools behind this port means they are dumb actuators (act + wait
// for the expected state) with no chromedp coupling — and the entire
// flow is exercisable without launching a browser.
//
// The Wait* methods are how the tools confirm an action's effect AND ride
// out the async navigation it triggers (a login POST + redirect, an AJAX
// vote), rather than racing it with an instant DOM query. They return nil
// once the condition holds and an error if it doesn't within the
// adapter's bounded action timeout.
type Page interface {
	Goto(ctx context.Context, url string) error
	Click(ctx context.Context, selector string) error
	Fill(ctx context.Context, selector, value string) error
	// WaitVisible blocks until selector is present and visible.
	WaitVisible(ctx context.Context, selector string) error
	// Text returns the visible text of the first node matching selector.
	Text(ctx context.Context, selector string) (string, error)
	// Eval evaluates a JavaScript boolean expression in the page and
	// returns its result. Needed for state CSS selectors / visibility
	// waits can't express — notably whether an element is
	// visibility:hidden, which chromedp's WaitNotVisible misses because
	// such elements still occupy a layout box. HN hides the upvote arrow
	// exactly that way after a vote.
	Eval(ctx context.Context, js string) (bool, error)
	URL(ctx context.Context) (string, error)
}
