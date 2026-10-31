package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
)

var _ blobguard.Watch = (*Store)(nil)

// Running implements blobguard.Watch: a backup in the running state, or else a
// restore in the checking or applying state, read in one statement.
func (s *Store) Running(ctx context.Context) (blobguard.Work, bool, error) {
	row, err := s.queries.RunningWork(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return blobguard.Work{Kind: "", ID: ""}, false, nil
	}

	if err != nil {
		return blobguard.Work{Kind: "", ID: ""}, false, fmt.Errorf("select running backup or restore: %w", err)
	}

	return blobguard.Work{Kind: blobguard.Kind(row.Kind), ID: row.ID}, true, nil
}
