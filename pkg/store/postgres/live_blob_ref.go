package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
)

// The store reads the live row of a blob for the repair of a restore.
var _ backup.LiveBlobReader = (*Store)(nil)

// LiveBlobRef implements backup.LiveBlobReader. It reads the blob references
// of the live data in one snapshot, as a backup reads them, and returns the
// one of key. It wraps backup.ErrNotFound when no live row names key.
func (s *Store) LiveBlobRef(ctx context.Context, key string) (backup.BlobRef, error) {
	refs := []backup.BlobRef{}

	err := s.inSnapshotConn(ctx, func(tx pgx.Tx) error {
		attrs, err := backupAttrs(ctx, s.queries.WithTx(tx), tx)
		if err != nil {
			return err
		}

		refs = attrs.blobs

		return nil
	})
	if err != nil {
		return backup.BlobRef{}, err
	}

	return backup.FindBlobRef(refs, key)
}
