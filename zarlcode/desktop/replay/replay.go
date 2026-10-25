// Package replay exposes an immutable, canonical-transcript demonstration to
// the desktop prototype. It opens no workspace, provider, vault, or database.
package replay

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/zarldev/zarlmono/zarlcode/transcript"
)

// ErrFrameRange identifies an out-of-range replay cursor from the frontend.
var ErrFrameRange = errors.New("replay frame out of range")

// Entry is a bounded display projection of one canonical transcript occurrence.
// IDs, rather than provider tool identifiers, preserve occurrence identity.
type Entry struct {
	ID       string             `json:"id"`
	ParentID string             `json:"parentId"`
	TurnID   string             `json:"turnId"`
	Kind     string             `json:"kind"`
	Payload  transcript.Payload `json:"payload"`
}

// Frame is one independently recoverable replay position. Revision is a string
// to avoid losing uint64 precision at the JavaScript boundary.
type Frame struct {
	Index    int     `json:"index"`
	Total    int     `json:"total"`
	Revision string  `json:"revision"`
	Entries  []Entry `json:"entries"`
}

// Service serves deterministic frames over the Wails bridge. It owns only
// immutable threads; requests may run concurrently and start no goroutines.
type Service struct {
	frames []transcript.Thread
}

// New constructs the scripted replay using the application's canonical reducer.
// Reducer failures surface instead of silently returning a partial fixture.
func New() (*Service, error) {
	r := transcript.NewReducer()
	events := scenario()
	s := &Service{frames: make([]transcript.Thread, 0, len(events))}
	for i, event := range events {
		if _, err := r.Apply(event); err != nil {
			return nil, fmt.Errorf("replay event %d: %w", i, err)
		}
		s.frames = append(s.frames, r.Thread())
	}
	return s, nil
}

// Frame returns a complete, independent display snapshot for the requested
// position. A reload can request any position without replaying prior calls.
func (s *Service) Frame(index int) (Frame, error) {
	if index < 0 || index >= len(s.frames) {
		return Frame{}, fmt.Errorf("%w: %d", ErrFrameRange, index)
	}
	thread := s.frames[index]
	entries := thread.Entries()
	frame := Frame{
		Index: index, Total: len(s.frames), Revision: strconv.FormatUint(thread.Revision(), 10),
		Entries: make([]Entry, 0, len(entries)),
	}
	for _, entry := range entries {
		frame.Entries = append(frame.Entries, Entry{
			ID: entry.ID, ParentID: entry.ParentID, TurnID: entry.TurnID,
			Kind: entry.Kind.String(), Payload: entry.Payload,
		})
	}
	return frame, nil
}
