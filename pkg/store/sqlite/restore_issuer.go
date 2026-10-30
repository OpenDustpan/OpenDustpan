package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/backup"
)

// notKept selects the namespaces that lose their issuer keys in a restore: all
// except those whose primary installation is the one that restores. An empty
// installation keeps none, and a key whose namespace row is missing is not
// kept either.
const notKept = `namespace_id NOT IN (SELECT id FROM namespaces WHERE primary_installation = ?1 AND ?1 <> '')`

// DropStagedIssuerKeys implements backup.RestoreStaging. The decision is for
// each namespace by its primary installation in the staged data, and not for
// the backup as a whole: a backup that holds the namespaces of two
// installations keeps the keys of one and drops the keys of the other, so two
// installations never sign with one key. The live data is untouched.
func (s *Store) DropStagedIssuerKeys(
	ctx context.Context, a backup.StagingArea, keep string,
) ([]backup.IssuerKeysDropped, error) {
	path, err := s.areaPath(a)
	if err != nil {
		return nil, err
	}

	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return nil, err
	}

	dropped, err := dropInTx(ctx, conn, keep)

	if closeErr := conn.Close(); closeErr != nil {
		return nil, errors.Join(err, fmt.Errorf("close the staged file: %w", backup.ErrExportRead))
	}

	return dropped, err
}

// dropInTx runs the drop in one transaction of the staged file.
func dropInTx(ctx context.Context, conn *sql.DB, keep string) ([]backup.IssuerKeysDropped, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin the drop of the issuer keys: %w", backup.ErrExportRead)
	}

	dropped, err := dropIssuerKeysIn(ctx, tx, keep)
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit the drop of the issuer keys: %w", backup.ErrExportRead)
	}

	return dropped, nil
}

// dropIssuerKeysIn lists, then deletes, the issuer keys and the copy checks of
// the namespaces that keep does not keep.
func dropIssuerKeysIn(ctx context.Context, tx *sql.Tx, keep string) ([]backup.IssuerKeysDropped, error) {
	counts := map[string]int64{}

	for _, query := range []string{
		`SELECT COALESCE(n.name, k.namespace_id), COUNT(*) FROM issuer_keys k
			LEFT JOIN namespaces n ON n.id = k.namespace_id
			WHERE k.` + notKept + ` GROUP BY k.namespace_id, n.name`,
		`SELECT COALESCE(n.name, c.namespace_id), 0 FROM issuer_settings c
			LEFT JOIN namespaces n ON n.id = c.namespace_id
			WHERE c.copy_check <> '' AND c.` + notKept,
	} {
		if err := collectDrops(ctx, tx, query, keep, counts); err != nil {
			return nil, err
		}
	}

	for _, stmt := range []string{
		`DELETE FROM issuer_keys WHERE ` + notKept,
		`UPDATE issuer_settings SET copy_check = '' WHERE ` + notKept,
	} {
		if _, err := tx.ExecContext(ctx, stmt, keep); err != nil {
			return nil, fmt.Errorf("drop the issuer keys of the staged file: %w", backup.ErrExportRead)
		}
	}

	out := make([]backup.IssuerKeysDropped, 0, len(counts))
	for name, n := range counts {
		out = append(out, backup.IssuerKeysDropped{Namespace: name, Keys: n})
	}

	slices.SortFunc(out, func(a, b backup.IssuerKeysDropped) int { return strings.Compare(a.Namespace, b.Namespace) })

	return out, nil
}

// collectDrops adds the rows of query to counts: a namespace and a key count.
func collectDrops(ctx context.Context, tx *sql.Tx, query, keep string, counts map[string]int64) (err error) {
	rows, err := tx.QueryContext(ctx, query, keep)
	if err != nil {
		return fmt.Errorf("list the issuer keys of the staged file: %w", backup.ErrExportRead)
	}

	defer func() { err = errors.Join(err, rows.Close()) }()

	for rows.Next() {
		var (
			name string
			n    int64
		)

		if scanErr := rows.Scan(&name, &n); scanErr != nil {
			return fmt.Errorf("read the issuer keys of the staged file: %w", backup.ErrExportRead)
		}

		counts[name] += n
	}

	if rows.Err() != nil {
		return fmt.Errorf("read the issuer keys of the staged file: %w", backup.ErrExportRead)
	}

	return nil
}
