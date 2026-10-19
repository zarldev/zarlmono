// Package board retains bounded, immutable findings for one collaborative goal.
// Membership, delivery, and activation belong to the consuming goal owner.
package board

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ID is a board-local, increasing post identity. Zero means no post.
type ID uint64

// Author identifies the host-bound writer, not a model-selected identity.
type Author string

const (
	// MaxSummaryBytes bounds metadata copied into peer notifications.
	MaxSummaryBytes = 512
	// MaxBodyBytes bounds an individual retained finding.
	MaxBodyBytes = 16 << 10
	// MaxTopicBytes bounds an optional label.
	MaxTopicBytes = 128
	// MaxPage bounds metadata pages and full-body read batches.
	MaxPage = 32
)

var (
	// ErrInvalidDraft reports malformed or oversized external content.
	ErrInvalidDraft = errors.New("invalid board draft")
	// ErrNotFound reports an unknown post or reply target.
	ErrNotFound = errors.New("board post not found")
	// ErrCapacity reports exhausted retention limits.
	ErrCapacity = errors.New("board capacity exhausted")
	// ErrInvalidPage reports invalid pagination or batch bounds.
	ErrInvalidPage = errors.New("invalid board page")
)

// Draft is validated content without host-owned identity or authorship.
type Draft struct {
	summary string
	body    string
	topic   string
	replyTo ID
}

// ParseDraft validates model/user content at its entry boundary. Append checks
// that the reply target belongs to the receiving board.
func ParseDraft(summary, body, topic string, replyTo ID) (Draft, error) {
	if strings.TrimSpace(summary) == "" || strings.TrimSpace(body) == "" ||
		len(summary) > MaxSummaryBytes || len(body) > MaxBodyBytes || len(topic) > MaxTopicBytes ||
		!utf8.ValidString(summary) || !utf8.ValidString(body) || !utf8.ValidString(topic) {
		return Draft{}, ErrInvalidDraft
	}
	return Draft{summary: summary, body: body, topic: topic, replyTo: replyTo}, nil
}

// Summary is notification/listing metadata; it never contains the full body.
type Summary struct {
	ID      ID        `json:"id"`
	Author  Author    `json:"author"`
	Summary string    `json:"summary"`
	Topic   string    `json:"topic,omitempty"`
	ReplyTo ID        `json:"reply_to,omitempty"`
	Created time.Time `json:"created"`
}

// Post retains a finding, including its explicitly readable body.
type Post struct {
	Summary
	Body string `json:"body"`
}

// Limits bounds one board's lifetime retention. The host supplies positive limits.
// Capacity exhaustion rejects new posts rather than silently evicting evidence.
type Limits struct {
	Posts int
	Bytes int
}

// DefaultLimits permits 1024 findings and at most 8 MiB of retained text.
func DefaultLimits() Limits { return Limits{Posts: 1024, Bytes: 8 << 20} }

// Page is a metadata snapshot. Next is the last inspected sequence, including
// when a topic filter returns no entries.
type Page struct {
	Posts []Summary `json:"posts"`
	Next  ID        `json:"next"`
	More  bool      `json:"more"`
}

// Store owns an append-only in-memory board. Snapshots contain only values and
// immutable strings. A Store must not be copied after use.
type Store struct {
	mu     sync.Mutex
	limits Limits
	posts  []Post
	bytes  int
}

// New constructs an empty board with the host's retention policy.
func New(limits Limits) *Store { return &Store{limits: limits} }

// Append atomically checks reply membership and capacity before allocating an
// identity. Rejected writes do not consume IDs. Author is supplied by the host.
func (s *Store) Append(author Author, draft Draft) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if draft.replyTo > ID(len(s.posts)) {
		return Summary{}, ErrNotFound
	}
	size := len(draft.summary) + len(draft.body) + len(draft.topic) + len(author)
	if len(s.posts) >= s.limits.Posts || size > s.limits.Bytes-s.bytes {
		return Summary{}, ErrCapacity
	}
	metadata := Summary{ID: ID(len(s.posts) + 1), Author: author, Summary: draft.summary,
		Topic: draft.topic, ReplyTo: draft.replyTo, Created: time.Now()}
	s.posts = append(s.posts, Post{Summary: metadata, Body: draft.body})
	s.bytes += size
	return metadata, nil
}

// List returns increasing IDs after the cursor, optionally filtered by topic.
// Limits must be in [1, MaxPage]; future cursors are rejected.
func (s *Store) List(after ID, limit int, topic string) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 || limit > MaxPage || after > ID(len(s.posts)) {
		return Page{}, ErrInvalidPage
	}
	page := Page{Next: after}
	for _, post := range s.posts[after:] {
		page.Next = post.ID
		if topic == "" || topic == post.Topic {
			page.Posts = append(page.Posts, post.Summary)
		}
		if len(page.Posts) == limit {
			break
		}
	}
	page.More = page.Next < ID(len(s.posts))
	return page, nil
}

// Read returns full records in requested order. Any missing ID rejects the whole
// batch. A batch must contain between one and MaxPage IDs.
func (s *Store) Read(ids []ID) ([]Post, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) < 1 || len(ids) > MaxPage {
		return nil, ErrInvalidPage
	}
	posts := make([]Post, 0, len(ids))
	for _, id := range ids {
		if id == 0 || id > ID(len(s.posts)) {
			return nil, ErrNotFound
		}
		posts = append(posts, s.posts[id-1])
	}
	return posts, nil
}
