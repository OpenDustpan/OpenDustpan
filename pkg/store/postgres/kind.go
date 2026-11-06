package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	if !kind.Valid() {
		return "", fmt.Errorf("%w: %q", installation.ErrInvalidKind, kind)
	}

	_, err := s.queries.ClaimInstallationKind(ctx, pgtype.Text{String: kind.String(), Valid: true})
	if err != nil {
		return "", fmt.Errorf(
			"store installation kind postgres database: %w",
			storeerr.Sentinel(installation.ErrUnavailable, err),
		)
	}

	raw, err := s.queries.GetInstallationKind(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf(
			"select installation kind postgres database: row is missing: %w", installation.ErrUnavailable,
		)
	}

	if err != nil {
		return "", fmt.Errorf(
			"select installation kind postgres database: %w",
			storeerr.Sentinel(installation.ErrUnavailable, err),
		)
	}

	return installation.FromStored(raw.String)
}

// checkKind compares the kind in the database with the kind of the command
// before the migrations run. The caller holds the migration lock in tx. It
// does nothing without a command kind, and on a new database with no
// installation table: the baseline makes the table, and the claim decides.
// An installation table without the kind column fails closed with
// installation.ErrUnavailable: the baseline makes the column, so such a table
// is damaged, and an unknown kind is never a dev kind.
func checkKind(ctx context.Context, tx pgx.Tx, want installation.Kind) error {
	if want == "" {
		return nil
	}

	var table bool

	if err := tx.QueryRow(ctx, `SELECT to_regclass('installation') IS NOT NULL`).Scan(&table); err != nil {
		return fmt.Errorf("check installation table: %w", err)
	}

	if !table {
		return nil
	}

	var present bool

	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_attribute
		WHERE attrelid = to_regclass('installation') AND attname = 'kind' AND NOT attisdropped
	)`).Scan(&present)
	if err != nil {
		return fmt.Errorf("check installation columns: %w", err)
	}

	if !present {
		return fmt.Errorf("read installation kind: the installation table has no kind column: %w",
			installation.ErrUnavailable)
	}

	var raw pgtype.Text

	err = tx.QueryRow(ctx, `SELECT kind FROM installation WHERE id = 1`).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read installation kind: row is missing: %w", installation.ErrUnavailable)
	}

	if err != nil {
		return fmt.Errorf("read installation kind: %w", storeerr.Sentinel(installation.ErrUnavailable, err))
	}

	return installation.Check(raw.String, want)
}

var _ backup.KindReader = (*Store)(nil)

// ReadKind implements backup.KindReader. It reads the stored kind and never
// claims one. It returns installation.ErrUnavailable, without driver text,
// when the row is missing, when no kind is claimed yet, when the stored value
// is not a known kind, and when the read fails.
func (s *Store) ReadKind(ctx context.Context) (installation.Kind, error) {
	raw, err := s.queries.GetInstallationKind(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf(
			"read installation kind postgres database: row is missing: %w", installation.ErrUnavailable,
		)
	}

	if err != nil {
		return "", fmt.Errorf(
			"read installation kind postgres database: %w",
			storeerr.Sentinel(installation.ErrUnavailable, err),
		)
	}

	return installation.FromStored(raw.String)
}
