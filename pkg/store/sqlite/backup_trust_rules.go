package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
)

// sqliteTrustRuleBackupTables holds the backup columns of the trust rule
// table, in the order of backup.Tables.
func sqliteTrustRuleBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: backup.TableTrustRules,
			columns: []string{
				"id", "namespace", backupColName, backupColIssuer, "audience", "key_source", "static_keys",
				"conditions_json", backupColPrincipalID, backupColCreatedBy, backupColCreatedAtMs,
			},
		},
	}
}

// checkStagedTrustRules checks each trust rule row of a staged backup with
// the rules of a new rule: the row passes the checks of a rule, and its
// principal is a principal of the staged backup that is not deleted, is not
// a person, and belongs to the namespace of the rule. A row that fails makes
// the backup corrupt, so a restore never brings in a rule that the server
// would not have stored. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it, and the
// exchange checks the principal again. This check stays so that a bad row
// fails the restore and does not wait for a reader.
func checkStagedTrustRules(ctx context.Context, conn *sql.DB) (err error) {
	const query = `SELECT r.id, r.namespace, r.name, r.issuer, r.audience, r.key_source, r.static_keys,
r.conditions_json, r.principal_id, r.created_by, r.created_at_ms, p.kind, p.namespace,
COALESCE(p.deleted_at_ms, 0), p.id IS NOT NULL
FROM trust_rules AS r LEFT JOIN principals AS p ON p.id = r.principal_id`

	rows, queryErr := conn.QueryContext(ctx, query)
	if queryErr != nil {
		return fmt.Errorf("read staged trust rules: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("read staged trust rules: %w", backup.ErrExportRead)
		}
	}()

	for rows.Next() {
		var (
			row             trustrulestore.Row
			kind, principNS sql.NullString
			deletedAtMs     int64
			found           bool
		)

		scanErr := rows.Scan(&row.ID, &row.Namespace, &row.Name, &row.Issuer, &row.Audience, &row.KeySource,
			&row.StaticKeys, &row.ConditionsJSON, &row.PrincipalID, &row.CreatedBy, &row.CreatedAtMs, &kind,
			&principNS, &deletedAtMs, &found)
		if scanErr != nil {
			return fmt.Errorf("read staged trust rules: %w", backup.ErrExportRead)
		}

		if _, ruleErr := trustrulestore.Rule(row); ruleErr != nil {
			return fmt.Errorf("staged trust rule %q is not valid: %w: %w", row.ID, ruleErr, backup.ErrCorrupt)
		}

		p := trustrulestore.PrincipalRow{
			Kind: kind.String, Namespace: principNS.String, DeletedAtMs: deletedAtMs, Found: found,
		}
		if pErr := trustrulestore.CheckPrincipal(p, namespace.Name(row.Namespace)); pErr != nil {
			return fmt.Errorf("staged trust rule %q: %w: %w", row.ID, pErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged trust rules: %w", backup.ErrExportRead)
	}

	return nil
}
