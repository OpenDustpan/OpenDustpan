package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestEventSealingConformance runs the sealing cases of the update events.
func TestEventSealingConformance(t *testing.T) {
	t.Parallel()

	storetest.RunEventSealing(t, func(t *testing.T) storetest.EventSealStore {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")
		copyTemplate(t, path)

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return &eventRowsStore{Store: s, path: path}
	})
}

// eventRowsStore is a store whose event rows a case reads and changes with
// direct SQL.
type eventRowsStore struct {
	*sqlite.Store

	path string
}

var _ storetest.EventRows = (*eventRowsStore)(nil)

// EventPayloads implements storetest.EventRows.
func (s *eventRowsStore) EventPayloads(ctx context.Context) ([][]byte, error) {
	conn, err := openRawDB(s.path)
	if err != nil {
		return nil, err
	}

	var out [][]byte

	err = readEventPayloads(ctx, conn, &out)
	if closeErr := conn.Close(); closeErr != nil {
		return nil, errors.Join(err, fmt.Errorf("close raw database: %w", closeErr))
	}

	return out, err
}

// readEventPayloads appends the payload of every event row to out, read
// through conn.
func readEventPayloads(ctx context.Context, conn *sql.DB, out *[][]byte) (err error) {
	rows, err := conn.QueryContext(ctx, `SELECT payload FROM update_events ORDER BY update_id, sequence`)
	if err != nil {
		return fmt.Errorf("select event rows: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close event rows: %w", closeErr))
		}
	}()

	for rows.Next() {
		var p []byte
		if err := rows.Scan(&p); err != nil {
			return fmt.Errorf("scan event row: %w", err)
		}

		*out = append(*out, p)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("read event rows: %w", err)
	}

	return nil
}

// SwapEventPayloads implements storetest.EventRows.
func (s *eventRowsStore) SwapEventPayloads(
	ctx context.Context, au stack.UpdateID, a int64, bu stack.UpdateID, b int64,
) (err error) {
	conn, err := openRawDB(s.path)
	if err != nil {
		return err
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close raw database: %w", closeErr))
		}
	}()

	const read = `SELECT payload FROM update_events WHERE update_id = ? AND sequence = ?`

	var pa, pb []byte
	if err := conn.QueryRowContext(ctx, read, au.String(), a).Scan(&pa); err != nil {
		return fmt.Errorf("read event row: %w", err)
	}

	if err := conn.QueryRowContext(ctx, read, bu.String(), b).Scan(&pb); err != nil {
		return fmt.Errorf("read event row: %w", err)
	}

	const write = `UPDATE update_events SET payload = ? WHERE update_id = ? AND sequence = ?`

	for _, w := range []struct {
		payload []byte
		update  stack.UpdateID
		seq     int64
	}{{pb, au, a}, {pa, bu, b}} {
		res, err := conn.ExecContext(ctx, write, w.payload, w.update.String(), w.seq)
		if err != nil {
			return fmt.Errorf("write event row: %w", err)
		}

		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("write event row: %d rows: %w", n, errors.Join(err, errSwapRows))
		}
	}

	return nil
}

// errSwapRows reports a write of the swap that did not change one row.
var errSwapRows = errors.New("the write did not change one row")
