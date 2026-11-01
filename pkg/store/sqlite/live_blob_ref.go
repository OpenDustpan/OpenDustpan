package sqlite

import (
	"context"

	"opendustpan.org/opendustpan/pkg/backup"
)

// The store reads the live row of a blob for the repair of a restore.
var _ backup.LiveBlobReader = (*Store)(nil)

// LiveBlobRef implements backup.LiveBlobReader. It reads the blob references
// of the live data, as a backup reads them, and returns the one of key. It
// wraps backup.ErrNotFound when no live row names key.
func (s *Store) LiveBlobRef(ctx context.Context, key string) (backup.BlobRef, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	refs, _, err := blobRefs(ctx, s.conn)
	if err != nil {
		return backup.BlobRef{}, err
	}

	return backup.FindBlobRef(refs, key)
}
