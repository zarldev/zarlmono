package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// Tool names and the HN selectors the actuators drive. The deterministic
// test swaps in a fake Page, so correctness of the flow doesn't depend on
// these matching the live site.
const (
	ToolUpvoteTop tools.ToolName = "hn_upvote_top"
	ToolLogin     tools.ToolName = "hn_login"

	hnURL      = "https://news.ycombinator.com/"
	hnLoginURL = "https://news.ycombinator.com/login"

	// The top post's upvote control is an anchor <a id="up_<storyid>">
	// wrapping a <div class="votearrow">. The first such anchor in DOM
	// order is the #1 story. HN's vote() JS hides this anchor once the
	// vote registers, so its disappearance is the "voted" signal.
	selTopVote = "a[id^='up_']"
	// The top post's title link — first .titleline anchor in DOM order,
	// the same #1 story selTopVote targets.
	selTopTitle = ".titleline a"

	selAcct     = "input[name='acct']"
	selPw       = "input[name='pw']"
	selLoginBtn = "input[type='submit']" // login form is first on /login
)

// votedExpr is true once the top post is upvoted. HN's vote() JS hides the
// up arrow with visibility:hidden (which chromedp's WaitNotVisible misses,
// since a hidden element still has a layout box) and may add an "unvote"
// link. This reads the actual post-vote signals: the arrow gone or hidden,
// or an unvote control present.
const votedExpr = `(function(){
  var up = document.querySelector("a[id^='up_']");
  if (!up) return true;
  var s = getComputedStyle(up);
  if (s.visibility === "hidden" || s.display === "none" || up.offsetParent === null) return true;
  return document.querySelector("a[id^='un_']") !== null;
})()`

// loggedInSelector is HN's most reliable authenticated marker: the top
// bar links your username to user?id=<account>. It only exists when
// logged in, so it cleanly tells a real login apart from a captcha /
// bad-credentials page (which re-renders /login without it).
func loggedInSelector(user string) string {
	return fmt.Sprintf("a[href*='user?id=%s']", user)
}

// upvoteTop is a dumb actuator: open HN, click the top post's upvote
// anchor, and confirm the vote registered by waiting for the anchor to
// disappear (HN hides it after a successful vote). It does NOT handle the
// login wall — the require_auth guardrail blocks it until the session is
// authenticated, so by the time Execute runs we're logged in.
type upvoteTop struct{ s *Session }

func (*upvoteTop) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolUpvoteTop,
		Description: "Upvote the current top post on Hacker News.",
		Parameters:  llm.SchemaFromMap(map[string]any{"type": "object", "properties": map[string]any{}}),
	}
}

func (t *upvoteTop) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	p := t.s.page
	if err := p.Goto(ctx, hnURL); err != nil {
		return tools.Failure(call.ID, fmt.Errorf("open HN: %w", err)), nil
	}
	// Capture what we're about to upvote (best-effort — a missing title
	// shouldn't sink the vote, which is the actual goal).
	title, _ := p.Text(ctx, selTopTitle)
	if err := p.Click(ctx, selTopVote); err != nil {
		return tools.Failure(call.ID, fmt.Errorf("click upvote arrow: %w", err)), nil
	}
	// The vote is AJAX, so the hide isn't instant — poll the voted signal
	// for a few seconds rather than racing it with a single read.
	if !t.pollVoted(ctx) {
		t.s.record("not_voted", false, "")
		return tools.Failure(call.ID, errors.New("vote did not register (top arrow still active after click)")), nil
	}
	t.s.record("upvoted", true, title)
	return tools.Success(call.ID, "top post upvoted: "+title), nil
}

// pollVoted reports whether votedExpr becomes true within a short window.
func (t *upvoteTop) pollVoted(ctx context.Context) bool {
	for range 20 {
		if voted, err := t.s.page.Eval(ctx, votedExpr); err == nil && voted {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(300 * time.Millisecond):
		}
	}
	return false
}

// login fills the HN login form, submits, and confirms authentication by
// waiting for the top-bar username link to appear (which also rides out
// the login POST + redirect). On success it flips the session's logged-in
// flag — exactly what require_auth gates the upvote on.
type login struct{ s *Session }

func (*login) Definition() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        ToolLogin,
		Description: "Log in to Hacker News with the configured account.",
		Parameters:  llm.SchemaFromMap(map[string]any{"type": "object", "properties": map[string]any{}}),
	}
}

func (t *login) Execute(ctx context.Context, call tools.ToolCall) (*tools.ToolResult, error) {
	if t.s.creds.User == "" || t.s.creds.Pass == "" {
		return tools.Failure(call.ID, errors.New("no Hacker News credentials configured (set HN_USER / HN_PASS)")), nil
	}
	p := t.s.page
	if err := p.Goto(ctx, hnLoginURL); err != nil {
		return tools.Failure(call.ID, fmt.Errorf("open login: %w", err)), nil
	}
	if err := p.Fill(ctx, selAcct, t.s.creds.User); err != nil {
		return tools.Failure(call.ID, fmt.Errorf("fill username: %w", err)), nil
	}
	if err := p.Fill(ctx, selPw, t.s.creds.Pass); err != nil {
		return tools.Failure(call.ID, fmt.Errorf("fill password: %w", err)), nil
	}
	if err := p.Click(ctx, selLoginBtn); err != nil {
		return tools.Failure(call.ID, fmt.Errorf("submit login: %w", err)), nil
	}
	if err := p.WaitVisible(ctx, loggedInSelector(t.s.creds.User)); err != nil {
		cur, _ := p.URL(ctx)
		return tools.Failure(call.ID, fmt.Errorf(
			"login not confirmed (page=%q): no user link for %q — likely bad credentials, a captcha, or a headless block: %w",
			cur, t.s.creds.User, err)), nil
	}
	t.s.setLoggedIn(true)
	return tools.Success(call.ID, "logged in"), nil
}
