package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// HistoryGCResult counts objects reclaimed by a committed maintenance transaction.
type HistoryGCResult struct {
	NodesDeleted  int64
	ValuesDeleted int64
}

// ReleaseCapturedHistory relinquishes an unpublished capture's durable pin.
// Publication consumes that same pin atomically. Release is idempotent; after
// release a detached reference is not protected from GC. Abandoned pins have no
// time-based expiry and must be released explicitly by their capture owner.
func (s *Store) ReleaseCapturedHistory(ctx context.Context, ref CheckpointHistory) error {
	if ref.PinID == "" {
		return nil
	}
	if err := s.q.ReleaseHistoryPin(ctx, gen.ReleaseHistoryPinParams{ID: ref.PinID,
		TranscriptHead: ref.TranscriptHead, ReplayHead: ref.ReplayHead, StateID: ref.StateID}); err != nil {
		return fmt.Errorf("release captured history: %w", err)
	}
	return nil
}

// GarbageCollectHistory explicitly reclaims immutable objects unreachable from
// sessions, checkpoints, model context, and unpublished capture pins. It starts
// no background work and is not called by retention or session deletion.
// A writer transaction precedes root discovery, including across Store handles.
// Missing/corrupt reachable objects or cancellation abort without reclamation.
// Batch receipts remain owned by their sessions and are never collected here.
func (s *Store) GarbageCollectHistory(ctx context.Context) (HistoryGCResult, error) {
	var result HistoryGCResult
	err := s.WithTx(ctx, func(tx *Store) error {
		if err := tx.q.AcquireCheckpointWrite(ctx); err != nil {
			return fmt.Errorf("acquire history GC write: %w", err)
		}
		heads, err := tx.q.ListHistoryRootHeads(ctx)
		if err != nil {
			return fmt.Errorf("read history GC heads: %w", err)
		}
		roots, err := tx.q.ListHistoryRootValues(ctx)
		if err != nil {
			return fmt.Errorf("read history GC values: %w", err)
		}
		mark := historyReachability{nodes: make(map[string]bool), values: make(map[string]bool)}
		for _, id := range roots {
			if err := mark.value(ctx, tx, id); err != nil {
				return err
			}
		}
		for _, head := range heads {
			if err := mark.chain(ctx, tx, head); err != nil {
				return err
			}
		}
		nodes, err := tx.q.ListHistoryNodeIDs(ctx)
		if err != nil {
			return fmt.Errorf("list history GC nodes: %w", err)
		}
		values, err := tx.q.ListHistoryValueIDs(ctx)
		if err != nil {
			return fmt.Errorf("list history GC values: %w", err)
		}
		for _, id := range nodes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if mark.nodes[id] {
				continue
			}
			count, err := tx.q.DeleteHistoryNode(ctx, id)
			if err != nil {
				return fmt.Errorf("delete unreachable history node: %w", err)
			}
			result.NodesDeleted += count
		}
		for _, id := range values {
			if err := ctx.Err(); err != nil {
				return err
			}
			if mark.values[id] {
				continue
			}
			count, err := tx.q.DeleteHistoryValue(ctx, id)
			if err != nil {
				return fmt.Errorf("delete unreachable history value: %w", err)
			}
			result.ValuesDeleted += count
		}
		return ctx.Err()
	})
	if err != nil {
		return HistoryGCResult{}, fmt.Errorf("collect history: %w", err)
	}
	return result, nil
}

type historyReachability struct {
	nodes  map[string]bool
	values map[string]bool
}

func (m *historyReachability) value(ctx context.Context, s *Store, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if id == "" {
		return ErrCheckpointCorrupt
	}
	if m.values[id] {
		return nil
	}
	if _, err := s.historyValue(ctx, id); err != nil {
		return err
	}
	m.values[id] = true
	return nil
}

func (m *historyReachability) chain(ctx context.Context, s *Store, head string) error {
	visiting := make(map[string]bool)
	for head != "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		if visiting[head] {
			return ErrCheckpointCorrupt
		}
		if m.nodes[head] {
			break
		}
		visiting[head] = true
		node, err := s.q.GetHistoryNode(ctx, head)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCheckpointUnavailable
		}
		if err != nil {
			return fmt.Errorf("read history GC node: %w", err)
		}
		if historyDigest([]byte(node.ParentID+":"+node.ValueID)) != head {
			return ErrCheckpointCorrupt
		}
		if err := m.value(ctx, s, node.ValueID); err != nil {
			return err
		}
		head = node.ParentID
	}
	for id := range visiting {
		m.nodes[id] = true
	}
	return nil
}
