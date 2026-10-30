package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/forgelinkstore"
)

// pgCopyForgeLinkTables holds the COPY columns of the tables of the Git host
// account links and of their requests, in the order of backup.Tables.
func pgCopyForgeLinkTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "forge_account_links",
			columns: []string{pgColKind, "host", "account_id", "login", pgColPrincipalID, "linked_at_ms"},
			orderBy: []string{pgColKind, "host", "account_id"},
		},
		{
			table: "forge_link_requests",
			columns: []string{
				"token_sha256", pgColPrincipalID, pgColCreatedAtMs, pgColExpiresAtMs, "proved_kind", "proved_host",
				"proved_account_id", "proved_login", "proved_delivery_id", "proved_at_ms", pgColUsedAtMs,
			},
			orderBy: []string{"token_sha256"},
		},
	}
}

// checkStagedForgeLinks checks each link and each link request of a staged
// backup with the rules of a stored row. A row that fails makes the backup
// corrupt, so a restore never brings in a link that the server would not serve.
// It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedForgeLinks(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	links, err := pool.Query(ctx, "SELECT kind, host, account_id, login, principal_id, linked_at_ms FROM "+
		stagingTable(staging, "forge_account_links"))
	if err != nil {
		return fmt.Errorf("read staged Git host account links: %w", backup.ErrExportRead)
	}

	defer links.Close()

	for links.Next() {
		var row forgelinkstore.LinkRow

		dest := []any{&row.Kind, &row.Host, &row.AccountID, &row.Login, &row.PrincipalID, &row.LinkedAtMs}

		if scanErr := links.Scan(dest...); scanErr != nil {
			return fmt.Errorf("read staged Git host account links: %w", backup.ErrExportRead)
		}

		if _, linkErr := forgelinkstore.Link(row); linkErr != nil {
			return fmt.Errorf("staged Git host account link is not valid: %w: %w", linkErr, backup.ErrCorrupt)
		}
	}

	if linksErr := links.Err(); linksErr != nil {
		return fmt.Errorf("read staged Git host account links: %w", backup.ErrExportRead)
	}

	return checkStagedForgeRequests(ctx, pool, staging)
}

// checkStagedForgeRequests checks each link request of a staged backup.
func checkStagedForgeRequests(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	rows, err := pool.Query(ctx, "SELECT token_sha256, principal_id, created_at_ms, expires_at_ms, proved_kind, "+
		"proved_host, proved_account_id, proved_login, proved_delivery_id, proved_at_ms, used_at_ms FROM "+
		stagingTable(staging, "forge_link_requests"))
	if err != nil {
		return fmt.Errorf("read staged link requests: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var row forgelinkstore.RequestRow

		scanErr := rows.Scan(
			&row.TokenSha256,
			&row.PrincipalID,
			&row.CreatedAtMs,
			&row.ExpiresAtMs,
			&row.ProvedKind,
			&row.ProvedHost,
			&row.ProvedAccountID,
			&row.ProvedLogin,
			&row.ProvedDeliveryID,
			&row.ProvedAtMs,
			&row.UsedAtMs,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged link requests: %w", backup.ErrExportRead)
		}

		if _, reqErr := forgelinkstore.Request(row); reqErr != nil {
			return fmt.Errorf("staged link request is not valid: %w: %w", reqErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged link requests: %w", backup.ErrExportRead)
	}

	return nil
}
