package postgres

// Columns that more than one runner table holds.
const (
	pgColSHA256      = "sha256"
	pgColRevokedAtMs = "revoked_at_ms"
	pgColOperationID = "operation_id"
	pgColNonce       = "nonce"
	pgColToken       = "token"
)

// pgCopyRunnerTables holds the COPY columns of the tables of runner pools,
// join tokens and runners, in the order of backup.Tables.
func pgCopyRunnerTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "runner_pools",
			columns: []string{
				"id",
				pgColNamespaceID,
				"name",
				"purpose",
				"lease_ms",
				"concurrency",
				"isolation",
				"git_mode",
				"git_helper",
				pgColCreatedAtMs,
				pgColUpdatedAtMs,
			},
			orderBy: []string{"id"},
		},
		{
			table: "runner_join_tokens",
			columns: []string{
				"id",
				pgColNamespaceID,
				"pool_id",
				pgColSHA256,
				pgColCreatedBy,
				pgColCreatedAtMs,
				pgColExpiresAtMs,
				pgColUsedAtMs,
				"used_by",
				pgColRevokedAtMs,
			},
			orderBy: []string{"id"},
		},
		{
			table: "runners",
			columns: []string{
				"id",
				pgColNamespaceID,
				"pool_id",
				"name",
				pgColKind,
				"isolation",
				"labels",
				"release",
				"key_public",
				pgColKeyCreatedAtMs,
				"previous_key_public",
				"previous_key_created_at_ms",
				"rotated_at_ms",
				"joined_with",
				"draining",
				"retired_at_ms",
				"last_seen_at_ms",
				"joined_at_ms",
			},
			orderBy: []string{"id"},
		},
	}
}

// pgRunnerControlTables lists the column lists of the control tables of the
// runner protocol.
func pgRunnerControlTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "runner_claims",
			columns: []string{pgColOperationID, pgColToken, "claimed_at_ms", pgColJobID, "phase"},
			orderBy: []string{pgColOperationID, pgColToken},
		},
		{
			table: "runner_handoff_tokens",
			columns: []string{
				pgColSHA256, pgColOperationID, pgColToken, "runner_id", "issued_at_ms", pgColExpiresAtMs, pgColUsedAtMs,
			},
			orderBy: []string{pgColSHA256},
		},
		{
			table: "runner_sessions",
			columns: []string{
				"id", pgColOperationID, pgColToken, "runner_id", "key_public", pgColKeyCreatedAtMs, pgColCreatedAtMs,
			},
			orderBy: []string{"id"},
		},
		{
			table:   "runner_nonces",
			columns: []string{"subject", pgColNonce, pgColExpiresAtMs},
			orderBy: []string{"subject", pgColNonce},
		},
	}
}
