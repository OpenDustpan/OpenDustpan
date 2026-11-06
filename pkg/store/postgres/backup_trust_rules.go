package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
)

// pgCopyTrustRuleTables holds the COPY columns of the trust rule table, in
// the order of backup.Tables.
func pgCopyTrustRuleTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: backup.TableTrustRules,
			columns: []string{
				"id", "namespace", pgColName, pgColIssuer, "audience", "key_source", "static_keys", "conditions_json",
				pgColPrincipalID, pgColCreatedBy, pgColCreatedAtMs,
			},
			orderBy: []string{"id"},
		},
	}
}

// checkStagedTrustRules checks each trust rule row of a staged backup with
// the rules of a new rule: the row passes the checks of a rule, and its
// principal is a principal of the staged backup that is not deleted, is a
// service principal, and belongs to the namespace of the rule. A row that fails makes
// the backup corrupt, so a restore never brings in a rule that the server
// would not have stored. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it, and the
// exchange checks the principal again. This check stays so that a bad row
// fails the restore and does not wait for a reader.
func checkStagedTrustRules(ctx context.Context, pool pgQuerier, staging string) error {
	query := "SELECT r.id, r.namespace, r.name, r.issuer, r.audience, r.key_source, r.static_keys, " +
		"r.conditions_json, r.principal_id, r.created_by, r.created_at_ms, p.kind, p.namespace, " +
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
			&row.StaticKeys, &row.ConditionsJSON, &row.PrincipalID, &row.CreatedBy, &row.CreatedAtMs, &kind,
			&principNS, &deletedAtMs, &found)
		if scanErr != nil {
			return fmt.Errorf("read staged trust rules: %w", backup.ErrExportRead)
		}

		if err := checkStagedTrustRule(row, kind, principNS, deletedAtMs, found); err != nil {
			return err
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
