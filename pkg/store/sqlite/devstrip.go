package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/devstrip"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var (
	_ backup.DevStripper       = (*Store)(nil)
	_ backup.DevReportRecorder = (*Store)(nil)
)

// sqliteDevDialect matches in SQLite. lower() of SQLite folds the ASCII
// letters only, and substr counts characters, which are the 4 ASCII bytes of
// a dev prefix when they match.
//
//nolint:gochecknoglobals // A frozen pair of SQL fragments.
var sqliteDevDialect = devstrip.Dialect{
	Lower: func(expr string) string { return "lower(" + expr + ")" },
	Left4: func(expr string) string { return "substr(" + expr + ", 1, 4)" },
}

// checkStageKind reads the live kind from this database and applies
// backup.CheckStageKind.
func (s *Store) checkStageKind(ctx context.Context, opts backup.StageOptions) error {
	live, err := s.ReadKind(ctx)
	if err != nil {
		return fmt.Errorf("stage portable backup: %w", err)
	}

	return backup.CheckStageKind(opts, live)
}

// sqlTxQuerier adapts a transaction to devstrip.Querier.
type sqlTxQuerier struct {
	tx *sql.Tx
}

// Exec implements devstrip.Querier.
func (q sqlTxQuerier) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := q.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("run a statement on the staged file: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count the changed rows: %w", err)
	}

	return n, nil
}

// Query implements devstrip.Querier.
//
//nolint:ireturn,rowserrcheck // The Querier port returns the Rows interface, and the caller checks Rows.Err.
func (q sqlTxQuerier) Query(ctx context.Context, query string, args ...any) (devstrip.Rows, error) {
	rows, err := q.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query the staged file: %w", err)
	}

	return rows, nil
}

// StripDevStaged implements backup.DevStripper on the staged file. It never
// touches the live database. With DevStripDetect it removes nothing: the
// strip runs in a transaction that rolls back. With DevStripRemove it commits
// only after the count of dev rows in the staged file is zero.
func (s *Store) StripDevStaged(ctx context.Context, mode backup.DevStripMode) (backup.DevStripReport, error) {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingPath == "" {
		return backup.DevStripReport{}, backup.ErrCorrupt
	}

	report, err := stripStagedFile(ctx, s.stagingPath, mode)
	if err != nil {
		return backup.DevStripReport{}, errors.Join(backup.ErrDevStripIncomplete, err)
	}

	return report, nil
}

// stripStagedFile runs the strip in one transaction on the staged file at path.
func stripStagedFile(ctx context.Context, path string, mode backup.DevStripMode) (backup.DevStripReport, error) {
	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return backup.DevStripReport{}, err
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Best-effort close of the staging handle.
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return backup.DevStripReport{}, fmt.Errorf("begin the strip of the staged file: %w", backup.ErrExportRead)
	}

	q := sqlTxQuerier{tx: tx}

	report, err := devstrip.Run(ctx, q, sqliteDevDialect)
	if err != nil {
		return backup.DevStripReport{}, errors.Join(err, tx.Rollback())
	}

	if mode == backup.DevStripDetect {
		if rbErr := tx.Rollback(); rbErr != nil {
			return backup.DevStripReport{}, fmt.Errorf("roll back the detect run: %w", rbErr)
		}

		return report, nil
	}

	left, err := devstrip.Count(ctx, q, sqliteDevDialect)
	if err != nil {
		return backup.DevStripReport{}, errors.Join(err, tx.Rollback())
	}

	if left > 0 {
		return backup.DevStripReport{}, errors.Join(
			fmt.Errorf("%w: %d rows in the staged file", devstrip.ErrDevRowsRemain, left), tx.Rollback(),
		)
	}

	if err := tx.Commit(); err != nil {
		return backup.DevStripReport{}, fmt.Errorf("commit the strip of the staged file: %w", backup.ErrExportRead)
	}

	return report, nil
}

// checkSwapDevLocked is the check of the swap: it reads the live kind from
// this database and, on a production installation, counts the dev data in the
// whole staged file. It refuses the swap when the live kind cannot be read and
// when the count is above zero. The check does not read the options of the
// stage call, the manifest or the report of the strip. The caller holds the
// store write lock.
//
// Defense in depth: the stage call and the restore service check the kind, and
// the strip removes the dev data. This check stays so that a defect in them
// cannot bring dev people into a production installation.
func (s *Store) checkSwapDevLocked(ctx context.Context, staged string) error {
	live, err := s.readKindLocked(ctx)
	if err != nil {
		return fmt.Errorf("apply staged data: %w", err)
	}

	if live != installation.KindProduction {
		return nil
	}

	conn, err := openStagedAccess(ctx, staged)
	if err != nil {
		return err
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Best-effort close of the staging handle.
	}()

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}) //nolint:exhaustruct // Only the access mode is set.
	if err != nil {
		return fmt.Errorf("apply staged data: check for dev data: %w", backup.ErrExportRead)
	}

	n, countErr := devstrip.Count(ctx, sqlTxQuerier{tx: tx}, sqliteDevDialect)

	if rollbackErr := tx.Rollback(); rollbackErr != nil && countErr == nil {
		countErr = rollbackErr
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

// dropDanglingTempAdmins removes the rows of temp_admins in the staged file at
// path that name a principal that the principals table of the staged file does
// not hold. The caller has put the live principals into the staged file, so
// the rows that go are the rows that dangle against the live principals. It
// returns the count.
func dropDanglingTempAdmins(ctx context.Context, path string) (int64, error) {
	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return 0, err
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Best-effort close of the staging handle.
	}()

	res, err := conn.ExecContext(ctx,
		`DELETE FROM temp_admins WHERE principal_id NOT IN (SELECT id FROM principals)`)
	if err != nil {
		return 0, fmt.Errorf("drop dangling temporary administrators: %w", backup.ErrExportRead)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("drop dangling temporary administrators: %w", backup.ErrExportRead)
	}

	return n, nil
}

// RecordDevReport implements backup.DevReportRecorder.
func (s *Store) RecordDevReport(ctx context.Context, id backup.RestoreID, report backup.DevStripReport) error {
	text, err := codec.EncodeDevReport(report)
	if err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.SetRestoreDevReport(ctx, db.SetRestoreDevReportParams{DevReport: text, ID: id.String()})
	if err != nil {
		return fmt.Errorf("store restore dev report: %w", err)
	}

	if n == 0 {
		return backup.ErrRestoreNotFound
	}

	return nil
}
