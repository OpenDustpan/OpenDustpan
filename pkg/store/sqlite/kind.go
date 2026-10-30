package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

var _ installation.KindStore = (*Store)(nil)

// ClaimKind implements installation.KindStore. It stores kind in the seeded
// row in one conditional UPDATE that changes a NULL kind only, then reads the
// stored kind. It maps any driver failure to installation.ErrUnavailable
// without driver text.
func (s *Store) ClaimKind(ctx context.Context, kind installation.Kind) (installation.Kind, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !kind.Valid() {
		return "", fmt.Errorf("%w: %q", installation.ErrInvalidKind, kind)
	}

	_, err := s.queries.ClaimInstallationKind(ctx, sql.NullString{String: kind.String(), Valid: true})
	if err != nil {
		return "", fmt.Errorf(
			"store installation kind sqlite database: %w",
			storeerr.Sentinel(installation.ErrUnavailable, err),
		)
	}

	raw, err := s.queries.GetInstallationKind(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf(
			"select installation kind sqlite database: row is missing: %w", installation.ErrUnavailable,
		)
	}

	if err != nil {
		return "", fmt.Errorf(
			"select installation kind sqlite database: %w",
			storeerr.Sentinel(installation.ErrUnavailable, err),
		)
	}

	return installation.FromStored(raw.String)
}

// checkKind compares the kind in the database with the kind of the command
// before Open applies a migration. It does nothing without a command kind,
// and when the installation table has no kind column yet: the migrations add
// the column, and the claim decides.
func (s *Store) checkKind(ctx context.Context) error {
	if s.kind == "" {
		return nil
	}

	var columns int64

	err := s.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('installation') WHERE name = 'kind'`,
	).Scan(&columns)
	if err != nil {
		return fmt.Errorf("check installation columns: %w", err)
	}

	if columns == 0 {
		return nil
	}

	raw, err := s.queries.GetInstallationKind(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read installation kind: row is missing: %w", installation.ErrUnavailable)
	}

	if err != nil {
		return fmt.Errorf("read installation kind: %w", storeerr.Sentinel(installation.ErrUnavailable, err))
	}

	return installation.Check(raw.String, s.kind)
}

var _ backup.KindReader = (*Store)(nil)

// ReadKind implements backup.KindReader. It reads the stored kind and never
// claims one. It returns installation.ErrUnavailable, without driver text,
// when the row is missing, when no kind is claimed yet, when the stored value
// is not a known kind, and when the read fails.
func (s *Store) ReadKind(ctx context.Context) (installation.Kind, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.readKindLocked(ctx)
}

// readKindLocked reads the stored kind. The caller holds the store lock,
// shared or exclusive.
func (s *Store) readKindLocked(ctx context.Context) (installation.Kind, error) {
	raw, err := s.queries.GetInstallationKind(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf(
			"read installation kind sqlite database: row is missing: %w", installation.ErrUnavailable,
		)
	}

	if err != nil {
		return "", fmt.Errorf(
			"read installation kind sqlite database: %w",
			storeerr.Sentinel(installation.ErrUnavailable, err),
		)
	}

	return installation.FromStored(raw.String)
}
