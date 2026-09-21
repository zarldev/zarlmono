package main

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// newUpvoteTopTool clicks the top post's upvote and verifies the vote registered.
// The require_auth guardrail blocks it until the session is authenticated.
func newUpvoteTopTool(s *Session) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolUpvoteTop,
		Description: "Upvote the current top post on Hacker News.",
		Parameters:  tools.SchemaFor[struct{}](),
	}, func(ctx context.Context, _ struct{}) (string, error) {
		p := s.page
		if err := p.Goto(ctx, hnURL); err != nil {
			return "", fmt.Errorf("open HN: %w", err)
		}
		// A missing title shouldn't sink the vote, which is the actual goal.
		title, _ := p.Text(ctx, selTopTitle)
		if err := p.Click(ctx, selTopVote); err != nil {
			return "", fmt.Errorf("click upvote arrow: %w", err)
		}
		if !pollVoted(ctx, s) {
			s.record("not_voted", false, "")
			return "", errors.New("vote did not register (top arrow still active after click)")
		}
		s.record("upvoted", true, title)
		return "top post upvoted: " + title, nil
	})
}

// pollVoted reports whether votedExpr becomes true within a short window.
func pollVoted(ctx context.Context, s *Session) bool {
	for range 20 {
		if voted, err := s.page.Eval(ctx, votedExpr); err == nil && voted {
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

// newLoginTool confirms authentication before updating the session flag.
func newLoginTool(s *Session) tools.Tool {
	return tools.New(tools.ToolSpec{
		Name:        ToolLogin,
		Description: "Log in to Hacker News with the configured account.",
		Parameters:  tools.SchemaFor[struct{}](),
	}, func(ctx context.Context, _ struct{}) (string, error) {
		if s.creds.User == "" || s.creds.Pass == "" {
			return "", errors.New("no Hacker News credentials configured (set HN_USER / HN_PASS)")
		}
		p := s.page
		if err := p.Goto(ctx, hnLoginURL); err != nil {
			return "", fmt.Errorf("open login: %w", err)
		}
		if err := p.Fill(ctx, selAcct, s.creds.User); err != nil {
			return "", fmt.Errorf("fill username: %w", err)
		}
		if err := p.Fill(ctx, selPw, s.creds.Pass); err != nil {
			return "", fmt.Errorf("fill password: %w", err)
		}
		if err := p.Click(ctx, selLoginBtn); err != nil {
			return "", fmt.Errorf("submit login: %w", err)
		}
		if err := p.WaitVisible(ctx, loggedInSelector(s.creds.User)); err != nil {
			cur, _ := p.URL(ctx)
			return "", fmt.Errorf(
				"login not confirmed (page=%q): no user link for %q — likely bad credentials, a captcha, or a headless block: %w",
				cur, s.creds.User, err)
		}
		s.setLoggedIn(true)
		return "logged in", nil
	})
}
