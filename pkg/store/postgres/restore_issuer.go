package postgres

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
)

// notKept selects the namespaces that lose their issuer keys in a restore: all
// except those whose primary installation is the one that restores. An empty
// installation keeps none, and a key whose namespace row is missing is not
// kept either.
const notKept = `namespace_id NOT IN (SELECT id FROM namespaces WHERE primary_installation = $1::text AND $1::text <> '')`

// DropStagedIssuerKeys implements backup.RestoreStaging. The decision is for
// each namespace by its primary installation in the staged data, and not for
// the backup as a whole: a backup that holds the namespaces of two
// installations keeps the keys of one and drops the keys of the other, so two
// installations never sign with one key. The live data is untouched.
func (s *Store) DropStagedIssuerKeys(
	ctx context.Context, a backup.StagingArea, keep string,
) ([]backup.IssuerKeysDropped, error) {
	staging, err := areaSchema(a)
	if err != nil {
		return nil, err
	}

	if !hasStagingPrefix(staging) {
		return nil, fmt.Errorf("drop issuer keys in staging schema %q: %w", staging, backup.ErrCorrupt)
	}

	var dropped []backup.IssuerKeysDropped

	err = s.inLongTx(ctx, backupBound(), readWriteOpts(), func(tx pgx.Tx) error {
		if _, pathErr := setSearchPath(ctx, tx, staging); pathErr != nil {
			return pathErr
		}

		var dropErr error

		dropped, dropErr = dropIssuerKeysIn(ctx, tx, keep)

		return dropErr
	})
	if err != nil {
		return nil, err
	}

	return dropped, nil
}

// dropIssuerKeysIn lists, then deletes, the issuer keys and the copy checks of
// the namespaces that keep does not keep.
func dropIssuerKeysIn(ctx context.Context, tx pgx.Tx, keep string) ([]backup.IssuerKeysDropped, error) {
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
		if _, err := tx.Exec(ctx, stmt, keep); err != nil {
			return nil, fmt.Errorf("drop the issuer keys of the staging schema: %w", backup.ErrExportRead)
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
func collectDrops(ctx context.Context, tx pgx.Tx, query, keep string, counts map[string]int64) error {
	rows, err := tx.Query(ctx, query, keep)
	if err != nil {
		return fmt.Errorf("list the issuer keys of the staging schema: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			name string
			n    int64
		)

		if scanErr := rows.Scan(&name, &n); scanErr != nil {
			return fmt.Errorf("read the issuer keys of the staging schema: %w", backup.ErrExportRead)
		}

		counts[name] += n
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("read the issuer keys of the staging schema: %w", backup.ErrExportRead)
	}

	return nil
}
