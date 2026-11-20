package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// checkSwapMinimum refuses a swap when the binary of this store is below the
// highest minimum binary version of the live schema. The swap puts in a
// staging schema that this binary built, with its own schema_migrations. A
// binary below the minimum would lower the minimum and remove the tables that
// newer binaries use. The check runs in the swap transaction, after the swap
// lock and before the first rename, so no write can change the live schema
// between the check and the swap. It is a control-path check: the exemption
// for maintenance mode stays, because it reads no installation state.
func (s *Store) checkSwapMinimum(ctx context.Context, tx pgx.Tx) error {
	return s.checkTxMinimum(ctx, tx)
}

// checkTxMinimum reads the minimum binary versions of the live schema in tx
// and returns a *schema.MinimumError when the binary of this store is below
// the highest one. It fails closed with maintenance.ErrUnavailable when the
// read fails.
func (s *Store) checkTxMinimum(ctx context.Context, tx pgx.Tx) error {
	var minimums []string

	if err := tx.QueryRow(ctx, `SELECT `+minimumsSQL).Scan(&minimums); err != nil {
		return fmt.Errorf("read schema minimum postgres database: %w", maintenance.ErrUnavailable)
	}

	return checkWriteMinimum(minimums, s.binaryVersion)
}

// checkSwap runs the checks that come before the first write of a swap, in the
// swap transaction: the minimum binary version of the live schema, and then
// the dev data rules over the whole staging area.
func (s *Store) checkSwap(ctx context.Context, tx pgx.Tx, staging string) error {
	if err := s.checkSwapMinimum(ctx, tx); err != nil {
		return err
	}

	return checkSwapDev(ctx, tx, staging)
}

// CheckBinaryMinimum returns a *schema.MinimumError when the binary of this
// store is below the highest minimum binary version of the live schema. The
// start of a restore calls it, so a restore on a binary that is too old is
// refused before it stages anything. It reads no installation state, so it
// also works in maintenance mode.
func (s *Store) CheckBinaryMinimum(ctx context.Context) error {
	var minimums []string

	if err := s.reads.QueryRow(ctx, `SELECT `+minimumsSQL).Scan(&minimums); err != nil {
		return fmt.Errorf("read schema minimum postgres database: %w", maintenance.ErrUnavailable)
	}

	return checkWriteMinimum(minimums, s.binaryVersion)
}
