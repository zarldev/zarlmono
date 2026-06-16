package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zarldev/zarlmono/zkit/agent/pursue"
	"github.com/zarldev/zarlmono/zkit/agent/runner/runnertest"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

// fakePage is a scripted browser: it tracks whether the login form was
// submitted and the top arrow clicked, and answers Visible accordingly —
// logged-in once login was submitted, voted once the arrow was clicked.
// No real Chrome; the whole flow is deterministic.
type fakePage struct {
	loginSubmitted bool
	upvoteClicked  bool
	lastURL        string
}

func (p *fakePage) Goto(_ context.Context, url string) error { p.lastURL = url; return nil }
func (p *fakePage) Click(_ context.Context, sel string) error {
	switch sel {
	case selLoginBtn:
		p.loginSubmitted = true
	case selTopVote:
		p.upvoteClicked = true
	}
	return nil
}
func (p *fakePage) Fill(context.Context, string, string) error { return nil }
func (p *fakePage) URL(context.Context) (string, error)        { return p.lastURL, nil }

func (p *fakePage) Text(context.Context, string) (string, error) {
	return "Fake Top Post", nil
}

func (p *fakePage) WaitVisible(_ context.Context, sel string) error {
	// loggedInSelector is the dynamic user-link query; it appears once
	// login was submitted.
	if strings.Contains(sel, "user?id=") && p.loginSubmitted {
		return nil
	}
	return errors.New("not visible: " + sel)
}

// Eval answers the voted check: the arrow reads as hidden once the vote
// was cast.
func (p *fakePage) Eval(_ context.Context, _ string) (bool, error) {
	return p.upvoteClicked, nil
}

func TestLoginTool_AuthenticatesSession(t *testing.T) {
	sess := NewSession(&fakePage{}, Creds{User: "u", Pass: "p"})
	res, err := (&login{s: sess}).Execute(context.Background(), tools.ToolCall{})
	if err != nil || !res.Success {
		t.Fatalf("login: success=%v err=%v", res.Success, err)
	}
	if !sess.LoggedIn(context.Background()) {
		t.Fatal("session not marked logged in after successful login")
	}
}

func TestUpvoteTool_VerifiesVote(t *testing.T) {
	sess := NewSession(&fakePage{}, Creds{})
	res, err := (&upvoteTop{s: sess}).Execute(context.Background(), tools.ToolCall{})
	if err != nil || !res.Success {
		t.Fatalf("upvote: success=%v err=%v", res.Success, err)
	}
	if !sess.VerifiedUpvoted() {
		t.Fatal("session not marked verified-upvoted after a registered vote")
	}
	if got := sess.UpvotedTitle(); got != "Fake Top Post" {
		t.Fatalf("UpvotedTitle = %q; want the top post's title captured at vote time", got)
	}
}

// TestHNUpvote_DeterministicEndToEnd exercises the whole stack with no
// browser and no LLM: a scripted client plays the model (upvote → login →
// upvote → settle). The require_auth rail blocks the first upvote
// (unauthenticated), the login tool flips the session, the second upvote
// verifies the vote, and the harness oracle confirms success against the
// world rather than the model's word.
func TestHNUpvote_DeterministicEndToEnd(t *testing.T) {
	page := &fakePage{}
	sess := NewSession(page, Creds{User: "u", Pass: "p"})

	client := runnertest.NewClient([][]llm.CompletionChunk{
		// turn 1: try to upvote — require_auth blocks it (not logged in).
		{runnertest.ChunkToolCall("c1", string(ToolUpvoteTop), "{}"), runnertest.ChunkDone()},
		// turn 2: log in.
		{runnertest.ChunkToolCall("c2", string(ToolLogin), "{}"), runnertest.ChunkDone()},
		// turn 3: upvote again — now authenticated, the vote registers.
		{runnertest.ChunkToolCall("c3", string(ToolUpvoteTop), "{}"), runnertest.ChunkDone()},
		// turn 4: settle with no tool call so the run terminates.
		{runnertest.ChunkText("done — top post upvoted"), runnertest.ChunkDone()},
	})

	out := RunUpvote(context.Background(), client, sess, 1)
	if out.Err() != nil {
		t.Fatalf("RunUpvote: %v", out.Err())
	}
	if out.Status() != pursue.Statuses.SUCCEEDED {
		t.Fatalf("status = %s; want succeeded (oracle should confirm the verified upvote)", out.Status())
	}
	if !sess.VerifiedUpvoted() {
		t.Fatal("session should report a verified upvote")
	}
	if !page.loginSubmitted {
		t.Fatal("login was never submitted — the auth rail/login flow did not run")
	}
	if !page.upvoteClicked {
		t.Fatal("the top arrow was never clicked — the authenticated upvote did not run")
	}
}
