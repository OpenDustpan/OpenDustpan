package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/internal/devstrip"
)

var (
	_ backup.DevStripper = (*Store)(nil)
)

// pgDevDialect matches in PostgreSQL. lower() depends on the collation of the
// database and can fold a non-ASCII letter, so the match uses translate() with
// the 26 ASCII letters only.
//
//nolint:gochecknoglobals // A frozen pair of SQL fragments.
var pgDevDialect = devstrip.Dialect{
	Lower: func(expr string) string {
		return "translate(" + expr + ", 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz')"
	},
	Left4: func(expr string) string { return "left(" + expr + ", 4)" },
}

// errDetectRollback ends the transaction of a detect run, so it rolls back.
var errDetectRollback = errors.New("the detect run changes nothing")

// pgTxQuerier adapts a transaction to devstrip.Querier.
type pgTxQuerier struct {
	tx pgx.Tx
}

// Exec implements devstrip.Querier.
func (q pgTxQuerier) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	tag, err := q.tx.Exec(ctx, devstrip.Rebind(query), args...)
	if err != nil {
		return 0, fmt.Errorf("run a statement on the staging schema: %w", err)
	}

	return tag.RowsAffected(), nil
}

// Query implements devstrip.Querier.
//
//nolint:ireturn // The Querier port returns the Rows interface.
func (q pgTxQuerier) Query(ctx context.Context, query string, args ...any) (devstrip.Rows, error) {
	rows, err := q.tx.Query(ctx, devstrip.Rebind(query), args...)
	if err != nil {
		return nil, fmt.Errorf("run a query on the staging schema: %w", err)
	}

	return pgRows{Rows: rows}, nil
}

// pgRows adapts pgx.Rows to devstrip.Rows.
type pgRows struct {
	pgx.Rows
}

// Close implements devstrip.Rows.
func (r pgRows) Close() error {
	r.Rows.Close()

	return nil
}

// readWriteOpts returns the options of the transaction of the strip.
func readWriteOpts() pgx.TxOptions {
	//nolint:exhaustruct_v5 // The strip needs only isolation and access mode.
	return pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite}
}

// setSearchPath points the unqualified table names of tx at schema until the
// transaction ends, and returns the previous setting.
func setSearchPath(ctx context.Context, tx pgx.Tx, schema string) (string, error) {
	var previous string

	if err := tx.QueryRow(ctx, "SELECT current_setting('search_path')").Scan(&previous); err != nil {
		return "", fmt.Errorf("read the search path: %w", backup.ErrExportRead)
	}

	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", pgIdent(schema)); err != nil {
		return "", fmt.Errorf("set the staging search path: %w", backup.ErrExportRead)
	}

	return previous, nil
}

// restoreSearchPath sets the search path of tx back to previous.
func restoreSearchPath(ctx context.Context, tx pgx.Tx, previous string) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", previous); err != nil {
		return fmt.Errorf("restore the search path: %w", backup.ErrExportRead)
	}

	return nil
}

// StripDevStaged implements backup.DevStripper on the staging schema. It never
// touches the live schema. With DevStripDetect it removes nothing: the strip
// runs in a transaction that rolls back. With DevStripRemove it commits only
// after the count of dev rows in the staging schema is zero.
func (s *Store) StripDevStaged(ctx context.Context, mode backup.DevStripMode) (backup.DevStripReport, error) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingSchema == "" {
		return backup.DevStripReport{}, backup.ErrCorrupt
	}

	return s.stripSchema(ctx, s.stagingSchema, mode)
}

// stripSchema runs the dev data strip of StripDevStaged on the staging
// schema staging.
func (s *Store) stripSchema(
	ctx context.Context, staging string, mode backup.DevStripMode,
) (backup.DevStripReport, error) {
	if !hasStagingPrefix(staging) {
		return backup.DevStripReport{}, fmt.Errorf("strip staging schema %q: %w", staging, backup.ErrCorrupt)
	}

	var report backup.DevStripReport

	err := inBackupTx(ctx, s.pool, readWriteOpts(), func(tx pgx.Tx) error {
		if _, err := setSearchPath(ctx, tx, staging); err != nil {
			return err
		}

		q := pgTxQuerier{tx: tx}

		var runErr error

		report, runErr = devstrip.Run(ctx, q, pgDevDialect)
		if runErr != nil {
			return runErr
		}

		if mode == backup.DevStripDetect {
			return errDetectRollback
		}

		left, countErr := devstrip.Count(ctx, q, pgDevDialect)
		if countErr != nil {
			return countErr
		}

		if left > 0 {
			return fmt.Errorf("%w: %d rows in the staging schema", devstrip.ErrDevRowsRemain, left)
		}

		return nil
	})
	if errors.Is(err, errDetectRollback) {
		return report, nil
	}

	if err != nil {
		return backup.DevStripReport{}, errors.Join(backup.ErrDevStripIncomplete, err)
	}

	return report, nil
}

// checkSwapDev is the check of the swap, in the swap transaction tx: it reads
// the live kind from this database and, on a production installation, counts
// the dev data in the whole staging schema. It refuses the swap when the live
// kind cannot be read and when the count is above zero. It does not read the
// options of the stage call, the manifest or the report of the strip.
//
// Defense in depth: the stage call and the restore service check the kind, and
// the strip removes the dev data. This check stays so that a defect in them
// cannot bring dev people into a production installation.
func checkSwapDev(ctx context.Context, tx pgx.Tx, staging string) error {
	var raw pgtype.Text

	if err := tx.QueryRow(ctx, `SELECT kind FROM installation WHERE id = 1`).Scan(&raw); err != nil {
		return fmt.Errorf("apply staged data: read the live installation kind: %w", installation.ErrUnavailable)
	}

	live, err := installation.FromStored(raw.String)
	if err != nil {
		return fmt.Errorf("apply staged data: read the live installation kind: %w", err)
	}

	if live != installation.KindProduction {
		return nil
	}

	previous, err := setSearchPath(ctx, tx, staging)
	if err != nil {
		return err
	}

	n, countErr := devstrip.Count(ctx, pgTxQuerier{tx: tx}, pgDevDialect)

	if err := restoreSearchPath(ctx, tx, previous); err != nil {
		return err
	}

	if countErr != nil {
		joined := errors.Join(backup.ErrDevBackupRefused, countErr)

		return fmt.Errorf("apply staged data: check for dev data: %w", joined)
	}

	if n > 0 {
		return fmt.Errorf("apply staged data: %w", backup.ErrDevBackupRefused)
	}

	return nil
}

// dropDanglingTempAdmins removes the rows of temp_admins in the staging schema
// that name a principal that the principals table of the staging schema does
// not hold. The caller has put the live principals into the staging schema, so
// the rows that go are the rows that dangle against the live principals. It
// returns the count.
func dropDanglingTempAdmins(ctx context.Context, tx pgx.Tx, staging string) (int64, error) {
	schema := pgIdent(staging)

	tag, err := tx.Exec(ctx, "DELETE FROM "+schema+".temp_admins WHERE principal_id NOT IN (SELECT id FROM "+
		schema+".principals)")
	if err != nil {
		return 0, fmt.Errorf("drop dangling temporary administrators: %w", backup.ErrExportRead)
	}

	return tag.RowsAffected(), nil
}
