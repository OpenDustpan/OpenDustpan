package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ audit.EntryReader = (*Store)(nil)

// EntryAt implements audit.EntryReader.
func (s *Store) EntryAt(ctx context.Context, ns namespace.Name, seq int64) (audit.Entry, error) {
	row, err := s.queries.GetAuditEntry(ctx, db.GetAuditEntryParams{Namespace: ns.String(), Sequence: seq})
	if errors.Is(err, pgx.ErrNoRows) {
		return audit.Entry{}, fmt.Errorf("%w: %s at %d", audit.ErrEntryNotFound, ns, seq)
	}

	if err != nil {
		return audit.Entry{}, fmt.Errorf("read audit entry: %w", err)
	}

	return fromAuditRow(row), nil
}

// LastEntry implements audit.EntryReader.
func (s *Store) LastEntry(ctx context.Context, ns namespace.Name) (audit.Entry, error) {
	row, err := s.queries.LastAuditEntry(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return audit.Entry{}, fmt.Errorf("%w: %s holds no entry", audit.ErrEntryNotFound, ns)
	}

	if err != nil {
		return audit.Entry{}, fmt.Errorf("read last audit entry: %w", err)
	}

	return fromAuditRow(row), nil
}
