package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// pgCopyTrustRuleTables holds the COPY columns of the trust rule table, in
// the order of backup.Tables.
func pgCopyTrustRuleTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: backup.TableTrustRules,
			columns: []string{
				"id", pgColNamespace, pgColName, pgColIssuer, "audience", "key_source", "static_keys",
				"conditions_json", pgColPrincipalID, pgColCreatedBy, pgColCreatedAtMs, "revision",
				"spiffe_id_pattern", pgColBundleID,
			},
			orderBy: []string{"id"},
		},
		{
			table:   backup.TableTrustBundles,
			columns: []string{"id", pgColNamespace, "active_version", "revision", pgColCreatedAtMs},
			orderBy: []string{"id"},
		},
		{
			table:   backup.TableTrustBundleVersions,
			columns: []string{pgColBundleID, pgColVersion, "pem", pgColCreatedAtMs, "overlap_ends_at_ms"},
			orderBy: []string{pgColBundleID, pgColVersion},
		},
	}
}

// checkStagedTrustRules checks each trust rule row of a staged backup with
// the rules of a new rule: the row passes the checks of a rule, and its
// principal is a principal of the staged backup that is not deleted, is a
// service principal, and belongs to the namespace of the rule. A rule that
// names a bundle names the bundle of its own namespace. It also checks the
// staged trust bundles and their versions: each version passes the checks of
// a stored bundle version at restoreAt, each bundle counts at least its
// first revision, and the active version has a row. A row that fails makes
// the backup corrupt, so a restore never brings in a rule that the server
// would not have stored. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it, and the
// exchange checks the principal again. This check stays so that a bad row
// fails the restore and does not wait for a reader.
func checkStagedTrustRules(ctx context.Context, pool pgQuerier, staging string, restoreAt time.Time) error {
	bundles, err := checkStagedTrustBundles(ctx, pool, staging, restoreAt)
	if err != nil {
		return err
	}

	query := "SELECT r.id, r.namespace, r.name, r.issuer, r.audience, r.key_source, r.static_keys, " +
		"r.conditions_json, r.principal_id, r.created_by, r.created_at_ms, r.revision, r.spiffe_id_pattern, " +
		"COALESCE(r.bundle_id, ''), p.kind, p.namespace, " +
		"COALESCE(p.deleted_at_ms, 0), p.id IS NOT NULL FROM " + stagingTable(staging, backup.TableTrustRules) +
		" AS r LEFT JOIN " + stagingTable(staging, backup.TablePrincipals) + " AS p ON p.id = r.principal_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged trust rules: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			row             trustrulestore.Row
			kind, principNS *string
			deletedAtMs     int64
			found           bool
		)

		scanErr := rows.Scan(&row.ID, &row.Namespace, &row.Name, &row.Issuer, &row.Audience, &row.KeySource,
			&row.StaticKeys, &row.ConditionsJSON, &row.PrincipalID, &row.CreatedBy, &row.CreatedAtMs,
			&row.Revision, &row.SpiffeIDPattern, &row.BundleID, &kind, &principNS, &deletedAtMs, &found)
		if scanErr != nil {
			return fmt.Errorf("read staged trust rules: %w", backup.ErrExportRead)
		}

		if err := checkStagedTrustRule(row, kind, principNS, deletedAtMs, found); err != nil {
			return err
		}

		if bundleErr := checkStagedRuleBundle(row, bundles); bundleErr != nil {
			return bundleErr
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged trust rules: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedTrustRule checks one staged rule and its principal. kind and
// principNS are nil when the backup holds no principal with the ID of the
// rule.
func checkStagedTrustRule(row trustrulestore.Row, kind, principNS *string, deletedAtMs int64, found bool) error {
	if _, err := trustrulestore.Rule(row); err != nil {
		return fmt.Errorf("staged trust rule %q is not valid: %w: %w", row.ID, err, backup.ErrCorrupt)
	}

	p := trustrulestore.PrincipalRow{Kind: "", Namespace: "", DeletedAtMs: deletedAtMs, Found: found}
	if kind != nil {
		p.Kind = *kind
	}

	if principNS != nil {
		p.Namespace = *principNS
	}

	if err := trustrulestore.CheckPrincipal(p, namespace.Name(row.Namespace)); err != nil {
		return fmt.Errorf("staged trust rule %q: %w: %w", row.ID, err, backup.ErrCorrupt)
	}

	return nil
}

// checkStagedRuleBundle checks that a staged rule that names a bundle names
// the bundle of its own namespace. A bundle of another namespace, or no
// bundle at all, makes the backup corrupt.
func checkStagedRuleBundle(row trustrulestore.Row, bundles map[string]stagedTrustBundle) error {
	if row.BundleID == "" {
		return nil
	}

	b, ok := bundles[row.BundleID]
	if !ok || b.namespace != row.Namespace {
		return fmt.Errorf(
			"staged trust rule %q names bundle %q, which is not the bundle of the namespace %q: %w",
			row.ID, row.BundleID, row.Namespace, backup.ErrCorrupt,
		)
	}

	return nil
}

// stagedTrustBundle is a trust bundle of a staged backup with the numbers
// of its stored versions.
type stagedTrustBundle struct {
	namespace string
	active    int64
	versions  map[int64]bool
}

// checkStagedTrustBundles checks the staged trust bundles and their versions:
// each version holds PEM text of a stored bundle version within the size
// bound at restoreAt, each bundle counts at least its first revision, and
// the active version of each bundle has a row. It returns the bundles by ID
// for the check of the rules. A row that fails makes the backup corrupt.
func checkStagedTrustBundles(
	ctx context.Context, pool pgQuerier, staging string, restoreAt time.Time,
) (map[string]stagedTrustBundle, error) {
	bundles, err := readStagedTrustBundles(ctx, pool, staging)
	if err != nil {
		return nil, err
	}

	if err := readStagedTrustVersions(ctx, pool, staging, bundles, restoreAt); err != nil {
		return nil, err
	}

	for id, b := range bundles {
		if !b.versions[b.active] {
			return nil, fmt.Errorf(
				"staged trust bundle %q of %q has no row for its active version %d: %w",
				id, b.namespace, b.active, backup.ErrCorrupt,
			)
		}
	}

	return bundles, nil
}

// readStagedTrustBundles reads the staged trust bundles by ID. A bundle
// with a revision below 1 makes the backup corrupt.
func readStagedTrustBundles(
	ctx context.Context, pool pgQuerier, staging string,
) (map[string]stagedTrustBundle, error) {
	rows, err := pool.Query(
		ctx, "SELECT id, namespace, active_version, revision FROM "+stagingTable(staging, backup.TableTrustBundles),
	)
	if err != nil {
		return nil, fmt.Errorf("read staged trust bundles: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	bundles := map[string]stagedTrustBundle{}

	for rows.Next() {
		var id, ns string

		var active, revision int64

		if scanErr := rows.Scan(&id, &ns, &active, &revision); scanErr != nil {
			return nil, fmt.Errorf("read staged trust bundles: %w", backup.ErrExportRead)
		}

		if revision < 1 {
			return nil, fmt.Errorf(
				"staged trust bundle %q of %q has revision %d: %w", id, ns, revision, backup.ErrCorrupt,
			)
		}

		bundles[id] = stagedTrustBundle{namespace: ns, active: active, versions: map[int64]bool{}}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged trust bundles: %w", backup.ErrExportRead)
	}

	return bundles, nil
}

// readStagedTrustVersions reads the staged trust bundle versions into
// bundles: each version passes the checks of a stored bundle version within
// the size bound at restoreAt. A version that fails makes the backup
// corrupt.
func readStagedTrustVersions(
	ctx context.Context, pool pgQuerier, staging string,
	bundles map[string]stagedTrustBundle, restoreAt time.Time,
) error {
	rows, err := pool.Query(
		ctx, "SELECT bundle_id, version, pem FROM "+
			stagingTable(staging, backup.TableTrustBundleVersions)+" ORDER BY bundle_id, version",
	)
	if err != nil {
		return fmt.Errorf("read staged trust bundle versions: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var bundleID, pem string

		var version int64

		if scanErr := rows.Scan(&bundleID, &version, &pem); scanErr != nil {
			return fmt.Errorf("read staged trust bundle versions: %w", backup.ErrExportRead)
		}

		if versionErr := checkStagedTrustVersion(bundleID, version, pem, restoreAt, bundles); versionErr != nil {
			return versionErr
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged trust bundle versions: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedTrustVersion records one staged version row in bundles: the row
// names a bundle, counts at least version 1, and holds PEM text of a stored
// bundle version within the size bound at restoreAt. A version that fails
// makes the backup corrupt.
func checkStagedTrustVersion(
	bundleID string, version int64, pem string, restoreAt time.Time, bundles map[string]stagedTrustBundle,
) error {
	b, ok := bundles[bundleID]
	if !ok {
		return fmt.Errorf("staged trust bundle version %d names no bundle: %w", version, backup.ErrCorrupt)
	}

	if version < 1 {
		return fmt.Errorf(
			"staged trust bundle %q holds version %d: %w", bundleID, version, backup.ErrCorrupt,
		)
	}

	if len(pem) > trustrule.MaxBundleBytes {
		return fmt.Errorf(
			"staged trust bundle %q holds a version larger than %d bytes: %w",
			bundleID, trustrule.MaxBundleBytes, backup.ErrCorrupt,
		)
	}

	if _, parseErr := trustrule.ParseStoredBundle(pem, restoreAt); parseErr != nil {
		return fmt.Errorf("staged trust bundle %q: %w: %w", bundleID, parseErr, backup.ErrCorrupt)
	}

	b.versions[version] = true
	bundles[bundleID] = b

	return nil
}
