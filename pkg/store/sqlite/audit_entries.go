package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ audit.EntryReader = (*Store)(nil)

// EntryAt implements audit.EntryReader.
func (s *Store) EntryAt(ctx context.Context, ns namespace.Name, seq int64) (audit.Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetAuditEntry(ctx, db.GetAuditEntryParams{Namespace: ns.String(), Sequence: seq})
	if errors.Is(err, sql.ErrNoRows) {
		return audit.Entry{}, fmt.Errorf("%w: %s at %d", audit.ErrEntryNotFound, ns, seq)
	}

	if err != nil {
		return audit.Entry{}, fmt.Errorf("read audit entry: %w", err)
	}

	return fromAuditRow(row), nil
}
