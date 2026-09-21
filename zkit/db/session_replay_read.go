package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zarldev/zarlmono/zkit/db/gen"
)

// ReadSessionReplay returns independently owned, integrity-checked canonical
// occurrence bytes in order from the session's current immutable prefix, including
// shared ancestry. It does not require retained checkpoints or a surviving source.
// ErrNotFound identifies sessions without canonical replay (including legacy rows).
// Missing or corrupt history returns ErrCheckpointUnavailable or ErrCheckpointCorrupt.
func (s *Store) ReadSessionReplay(ctx context.Context, sessionID string) ([][]byte, error) {
	var replay [][]byte
	err := s.withReadTx(ctx, func(tx *Store) error {
		head, err := tx.q.GetHistoryHead(ctx, gen.GetHistoryHeadParams{SessionID: sessionID, Kind: replayHistoryKind})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read session replay head: %w", err)
		}
		replay, err = tx.readHistory(ctx, head)
		return err
	})
	return replay, err
}
