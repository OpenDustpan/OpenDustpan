package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestEventSealingConformance runs the sealing cases of the update events.
func TestEventSealingConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunEventSealing(t, func(t *testing.T) storetest.EventSealStore {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		s, err := postgres.Open(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(s.Close)

		return &eventRowsStore{Store: s, url: schemaURL}
	})
}

// eventRowsStore is a store whose event rows a case reads and changes with
// direct SQL.
type eventRowsStore struct {
	*postgres.Store

	url string
}

var _ storetest.EventRows = (*eventRowsStore)(nil)

// EventPayloads implements storetest.EventRows.
func (s *eventRowsStore) EventPayloads(ctx context.Context) ([][]byte, error) {
	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	out, err := readEventPayloads(ctx, conn)
	if closeErr := conn.Close(ctx); closeErr != nil {
		return nil, errors.Join(err, fmt.Errorf("close: %w", closeErr))
	}

	return out, err
}

// readEventPayloads reads the payload of every event row through conn.
func readEventPayloads(ctx context.Context, conn *pgx.Conn) ([][]byte, error) {
	rows, err := conn.Query(ctx, `SELECT payload FROM update_events ORDER BY update_id, sequence`)
	if err != nil {
		return nil, fmt.Errorf("select event rows: %w", err)
	}

	defer rows.Close()

	var out [][]byte

	for rows.Next() {
		var p []byte
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan event row: %w", err)
		}

		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read event rows: %w", err)
	}

	return out, nil
}

// SwapEventPayloads implements storetest.EventRows.
func (s *eventRowsStore) SwapEventPayloads(
	ctx context.Context, au stack.UpdateID, a int64, bu stack.UpdateID, b int64,
) (err error) {
	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", closeErr))
		}
	}()

	const swap = `UPDATE update_events AS e SET payload = o.payload
		FROM update_events AS o
		WHERE ((e.update_id = $1 AND e.sequence = $2 AND o.update_id = $3 AND o.sequence = $4)
			OR (e.update_id = $3 AND e.sequence = $4 AND o.update_id = $1 AND o.sequence = $2))`

	tag, err := conn.Exec(ctx, swap, au.String(), a, bu.String(), b)
	if err != nil {
		return fmt.Errorf("swap event rows: %w", err)
	}

	if tag.RowsAffected() != 2 {
		return fmt.Errorf("swap event rows: %d rows: %w", tag.RowsAffected(), errSwapRows)
	}

	return nil
}

// errSwapRows reports a swap that did not change two rows.
var errSwapRows = errors.New("the swap did not change two rows")
