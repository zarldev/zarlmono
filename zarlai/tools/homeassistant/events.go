package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// StateChange is the shape delivered to subscribers when Home Assistant
// reports an entity transition. NewState mirrors the payload we'd have fetched
// via REST at the moment of the change; OldState is provided so callers can
// compute diffs without a second lookup.
type StateChange struct {
	EntityID string
	NewState EntityState
	OldState EntityState
	FiredAt  time.Time
}

// StateHandler is invoked for every matching state_changed event.
type StateHandler func(StateChange)

// EventStream is a long-lived Home Assistant WebSocket connection that
// multiplexes state_changed events to per-entity subscribers. One stream is
// intended per process — add subscribers, don't open a second connection.
//
// The stream auto-reconnects on failure with exponential backoff. Subscribers
// are preserved across reconnects, so a registered handler keeps firing once
// the connection recovers without any subscriber-side plumbing.
type EventStream struct {
	wsURL string
	token string

	mu          sync.Mutex
	subscribers map[string][]StateHandler // entity_id -> handlers
	running     bool
	cancel      context.CancelFunc
}

// NewEventStream builds an EventStream from the REST base URL (http/https)
// and long-lived access token. The URL is rewritten to ws/wss + /api/websocket
// so callers don't have to juggle schemes.
func NewEventStream(baseURL, token string) (*EventStream, error) {
	wsURL, err := websocketURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("build websocket url: %w", err)
	}
	return &EventStream{
		wsURL:       wsURL,
		token:       token,
		subscribers: make(map[string][]StateHandler),
	}, nil
}

// Subscribe registers handler to be invoked every time entity_id's state
// changes. Safe to call before or after Start. Returns an unsubscribe func.
func (s *EventStream) Subscribe(entityID string, handler StateHandler) func() {
	s.mu.Lock()
	s.subscribers[entityID] = append(s.subscribers[entityID], handler)
	idx := len(s.subscribers[entityID]) - 1
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subs := s.subscribers[entityID]
		if idx >= len(subs) {
			return
		}
		s.subscribers[entityID] = append(subs[:idx], subs[idx+1:]...)
	}
}

// Start opens the WebSocket connection, authenticates, subscribes to all
// state_changed events, and runs the read loop until ctx is cancelled.
// Returns immediately — the connection is established on a goroutine. Callers
// receive no ready signal; subscribers will simply start firing once auth
// completes (sub-second on a healthy HA).
func (s *EventStream) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	go s.supervise(runCtx)
}

// Stop tears down the connection and cancels the read loop. Subscribers are
// preserved so a subsequent Start can resume without re-registering.
func (s *EventStream) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.running = false
}

// supervise runs one connection attempt at a time. On any exit it applies
// exponential backoff (capped at 60s) and reconnects until ctx ends.
func (s *EventStream) supervise(ctx context.Context) {
	backoff := time.Second
	for {
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("ha event stream dropped; reconnecting", "err", err, "delay", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 60*time.Second {
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
		}
	}
}

// runOnce performs one full connect → auth → subscribe → read cycle. Returns
// on any error or when ctx ends. Called in a loop by supervise.
func (s *EventStream) runOnce(ctx context.Context) error {
	conn, resp, err := websocket.Dial(ctx, s.wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() // handshake response; nothing to read on a 101 upgrade
	}
	// WebSocket messages from HA can reach tens of kilobytes for attribute-heavy
	// entities (media players with large cover-art URLs, stream metadata, etc.)
	// — lift the default 32KiB cap so we don't get "message too large" on them.
	conn.SetReadLimit(1 << 20)
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := s.authenticate(ctx, conn); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	if err := s.subscribeAllStateChanges(ctx, conn); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	slog.Info("ha event stream ready", "url", s.wsURL)

	for {
		var raw map[string]json.RawMessage
		if err := readJSON(ctx, conn, &raw); err != nil {
			return fmt.Errorf("read: %w", err)
		}
		if err := s.dispatch(raw); err != nil {
			slog.Warn("ha event dispatch failed", "err", err)
		}
	}
}

// authReply is the envelope HA uses for both auth_required and auth_ok/auth_invalid.
// We decode everything we care about in one shape — Message only populates on
// auth_invalid, where it carries the rejection reason.
type authReply struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
}

// authenticate completes HA's two-step WS handshake: wait for auth_required,
// reply with the access token, wait for auth_ok (or fail on auth_invalid).
func (s *EventStream) authenticate(ctx context.Context, conn *websocket.Conn) error {
	var hello authReply
	if err := readJSON(ctx, conn, &hello); err != nil {
		return fmt.Errorf("read auth_required: %w", err)
	}
	if hello.Type != "auth_required" {
		return fmt.Errorf("unexpected hello type: %q", hello.Type)
	}
	if err := writeJSON(ctx, conn, map[string]any{"type": "auth", "access_token": s.token}); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}
	var reply authReply
	if err := readJSON(ctx, conn, &reply); err != nil {
		return fmt.Errorf("read auth reply: %w", err)
	}
	if reply.Type != "auth_ok" {
		return fmt.Errorf("auth failed: %s (%s)", reply.Type, reply.Message)
	}
	return nil
}

// subscribeAllStateChanges asks HA to push every state_changed event. We
// filter by entity_id in dispatch; the bandwidth cost of receiving unfiltered
// events is tiny compared to managing N narrow subscriptions.
func (s *EventStream) subscribeAllStateChanges(ctx context.Context, conn *websocket.Conn) error {
	req := map[string]any{
		"id":         1,
		"type":       "subscribe_events",
		"event_type": "state_changed",
	}
	if err := writeJSON(ctx, conn, req); err != nil {
		return fmt.Errorf("send subscribe: %w", err)
	}
	var reply struct {
		Success bool            `json:"success"`
		Error   json.RawMessage `json:"error,omitempty"`
	}
	if err := readJSON(ctx, conn, &reply); err != nil {
		return fmt.Errorf("read subscribe reply: %w", err)
	}
	if !reply.Success {
		return fmt.Errorf("subscribe rejected: %s", reply.Error)
	}
	return nil
}

// dispatch decodes an incoming message and routes state_changed events to
// subscribers registered for the affected entity_id. Non-event frames (pongs,
// result acks) are ignored.
func (s *EventStream) dispatch(raw map[string]json.RawMessage) error {
	var typeStr string
	// Best-effort decode: frames without a parseable string "type" (pongs,
	// result acks) leave typeStr empty and fall through to the skip below.
	_ = json.Unmarshal(raw["type"], &typeStr)
	if typeStr != "event" {
		return nil
	}
	var env struct {
		Event struct {
			EventType string `json:"event_type"`
			Data      struct {
				EntityID string      `json:"entity_id"`
				NewState EntityState `json:"new_state"`
				OldState EntityState `json:"old_state"`
			} `json:"data"`
			TimeFired time.Time `json:"time_fired"`
		} `json:"event"`
	}
	if err := json.Unmarshal(raw["event"], &env.Event); err != nil {
		return fmt.Errorf("decode event body: %w", err)
	}
	if env.Event.EventType != "state_changed" {
		return nil
	}
	change := StateChange{
		EntityID: env.Event.Data.EntityID,
		NewState: env.Event.Data.NewState,
		OldState: env.Event.Data.OldState,
		FiredAt:  env.Event.TimeFired,
	}
	s.mu.Lock()
	handlers := append([]StateHandler{}, s.subscribers[change.EntityID]...)
	s.mu.Unlock()
	for _, h := range handlers {
		h(change)
	}
	return nil
}

// websocketURL rewrites an HA REST base URL into the WebSocket endpoint URL.
// http → ws, https → wss, path → /api/websocket.
func websocketURL(baseURL string) (string, error) {
	u, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
		// already correct
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path = "/api/websocket"
	return u.String(), nil
}

// readJSON reads one WebSocket message and decodes it into v. Trades a little
// performance for readability — we're parsing a handful of events per second.
func readJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}
