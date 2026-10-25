package swarm

import (
	"context"

	"github.com/zarldev/zarlmono/zkit/agent/board"
)

// Member is a host-bound capability for one agent and one goal. Model arguments
// cannot select another author or board. Copies retain the same authority.
type Member struct {
	owner *Swarm
	id    AgentID
}

// Post publishes a host-authored finding to every eligible member. It can awaken
// idle members; no model execution occurs on the publishing goroutine.
func (s *Swarm) Post(ctx context.Context, draft board.Draft) (board.Summary, error) {
	return s.post(ctx, "host", "", false, draft)
}

// Post publishes a member finding with a host-stamped author. It never wakes its
// own author, and stopped/suspended members cannot post new work.
func (m Member) Post(ctx context.Context, draft board.Draft) (board.Summary, error) {
	return m.owner.post(ctx, board.Author("agent/"+m.id), m.id, true, draft)
}

func (s *Swarm) post(ctx context.Context, author board.Author, writer AgentID, member bool, draft board.Draft) (board.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return board.Summary{}, err
	}
	if s.closed {
		return board.Summary{}, ErrClosed
	}
	if member {
		entry := s.members[writer]
		if entry.state == States.STOPPED || entry.state == States.SUSPENDED {
			return board.Summary{}, ErrClosed
		}
	}
	post, err := s.board.Append(author, draft)
	if err != nil {
		return board.Summary{}, err
	}
	for _, id := range s.order {
		entry := s.members[id]
		if (member && id == writer) || entry.state == States.STOPPED || entry.state == States.SUSPENDED {
			continue
		}
		entry.pending = append(entry.pending, post.ID)
		close(entry.changed)
		entry.changed = make(chan struct{})
		if entry.state == States.IDLE {
			entry.state = States.QUEUED
			_ = s.queue.Push(id)
		}
		s.members[id] = entry
	}
	s.signalLocked()
	return post, nil
}

// List returns bounded metadata without consuming pending notifications.
func (m Member) List(ctx context.Context, after board.ID, limit int, topic string) (board.Page, error) {
	if err := context.Cause(ctx); err != nil {
		return board.Page{}, err
	}
	return m.owner.board.List(after, limit, topic)
}

// Read retrieves bodies independently of notification admission. Another agent's
// read or notification cursor is never changed by this operation.
func (m Member) Read(ctx context.Context, ids []board.ID) ([]board.Post, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return m.owner.board.Read(ids)
}
