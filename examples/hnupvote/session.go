package main

import (
	"context"
	"sync"
)

// Creds is the Hacker News account the login tool uses.
type Creds struct{ User, Pass string }

// Session is the shared, mutable state the tools act on and the rails +
// oracle observe: the browser Page, the credentials, and the two facts
// the harness cares about — whether we're authenticated, and whether the
// top post is verified-upvoted. The require_auth guardrail reads
// LoggedIn; the harness oracle reads VerifiedUpvoted. Both are set by the
// tools after they confirm the corresponding state against the page, so
// the harness trusts the world, not the model's claims.
type Session struct {
	page  Page
	creds Creds

	mu              sync.Mutex
	loggedIn        bool
	verifiedUpvoted bool
	lastState       string
	upvotedTitle    string
}

// NewSession wires a session over the given Page and credentials.
func NewSession(page Page, creds Creds) *Session {
	return &Session{page: page, creds: creds}
}

// LoggedIn is the AuthCheck the require_auth guardrail consults before
// every guarded (effectful) tool call.
func (s *Session) LoggedIn(context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loggedIn
}

func (s *Session) setLoggedIn(b bool) {
	s.mu.Lock()
	s.loggedIn = b
	s.mu.Unlock()
}

// record stores the latest upvote observation. verified is true only
// when the tool confirmed the vote registered against the live page; on a
// verified vote it also captures the title of the post that was upvoted.
func (s *Session) record(state string, verified bool, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastState = state
	if verified {
		s.verifiedUpvoted = true
		s.upvotedTitle = title
	}
}

// VerifiedUpvoted is the harness oracle's success predicate.
func (s *Session) VerifiedUpvoted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifiedUpvoted
}

// UpvotedTitle is the title of the post the verified upvote landed on,
// empty until a vote registers.
func (s *Session) UpvotedTitle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upvotedTitle
}

// LastState surfaces the most recent upvote observation for the oracle's
// re-drive feedback.
func (s *Session) LastState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastState
}
