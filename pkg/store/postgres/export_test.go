package postgres //nolint:testpackage // The unexported write path of the setting must be reachable without its pre-check, so the exporter must stay in that package.

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// CheckSettingFence runs the comparison of the fence and the namespace that
// SetSetting makes before it opens a transaction.
func CheckSettingFence(ns namespace.Name, fence namespace.Fence) error {
	return settingFenceBinds(ns, fence)
}

// SettingWrite is the input of WriteSettingInTx.
type SettingWrite struct {
	Namespace namespace.Name
	Value     outputshare.Default
	Fence     namespace.Fence
	Entry     audit.Entry
}

// WriteSettingInTx writes the setting through the transaction of SetSetting
// and skips the pre-check, so a test reaches the layer inside the
// transaction alone.
func WriteSettingInTx(ctx context.Context, s *Store, w SettingWrite) error {
	return outputSharingSettings{s: s}.writeSetting(ctx, w.Namespace, w.Value, w.Fence, w.Entry)
}

// SourceFilterWrite is the input of WriteSourceFilterInTx.
type SourceFilterWrite struct {
	Namespace namespace.Name
	Text      string
	Fence     namespace.Fence
	Entry     audit.Entry
}

// WriteSourceFilterInTx writes the source filter through the transaction of
// SetFilter and skips the pre-check, so a test reaches the layer inside the
// transaction alone.
func WriteSourceFilterInTx(ctx context.Context, s *Store, w SourceFilterWrite) error {
	return SourceFilterStore{s: s}.writeFilter(ctx, w.Namespace, w.Text, w.Fence, w.Entry)
}

// runnerControlStatement is one write of the control rows of the runner
// protocol of the test hook: the SQL and its arguments.
type runnerControlStatement struct {
	stmt string
	args []any
}

// runnerControlStatements returns one row for each control table of the
// runner protocol, for the runner runnerID in the rows that name one. The
// control rows of a live session need a durable operation, so the test writes
// them through SQL.
func runnerControlStatements(runnerID string) []runnerControlStatement {
	return []runnerControlStatement{
		{
			stmt: `INSERT INTO runner_claims (operation_id, token, claimed_at_ms, job_id, phase) ` +
				`VALUES ('op-control', 1, 1, '', 0)`,
			args: nil,
		},
		{
			stmt: `INSERT INTO runner_handoff_tokens (sha256, operation_id, token, runner_id, issued_at_ms, ` +
				`expires_at_ms, used_at_ms) VALUES ('control-hash', 'op-control', 1, $1, 1, 2, 0)`,
			args: []any{runnerID},
		},
		{
			stmt: `INSERT INTO runner_sessions (id, operation_id, token, runner_id, key_public, ` +
				`key_created_at_ms, created_at_ms) VALUES ('control-session', 'op-control', 1, $1, ` +
				`decode('00', 'hex'), 1, 1)`,
			args: []any{runnerID},
		},
		{
			stmt: `INSERT INTO runner_nonces (subject, nonce, expires_at_ms) VALUES ('control', 'nonce', 2)`,
			args: nil,
		},
	}
}

// runnerControlTableNames lists the control tables that SeedRunnerControlRows
// writes and CountRunnerControlRows counts.
//
//nolint:gochecknoglobals // Fixed table names of the test hook, read only.
var runnerControlTableNames = []string{"runner_claims", "runner_handoff_tokens", "runner_sessions", "runner_nonces"}

// SeedRunnerControlRows writes one row in each control table of the runner
// protocol, for the runner runnerID in the rows that name one.
func SeedRunnerControlRows(ctx context.Context, s *Store, runnerID string) error {
	for _, stmt := range runnerControlStatements(runnerID) {
		if _, err := s.pool.Exec(ctx, stmt.stmt, stmt.args...); err != nil {
			return fmt.Errorf("seed runner control row: %w", err)
		}
	}

	return nil
}

// CountRunnerControlRows returns the number of rows in each control table of
// the runner protocol.
func CountRunnerControlRows(ctx context.Context, s *Store) (map[string]int64, error) {
	counts := make(map[string]int64, len(runnerControlTableNames))

	for _, table := range runnerControlTableNames {
		var n int64
		if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			return nil, fmt.Errorf("count rows of %s: %w", table, err)
		}

		counts[table] = n
	}

	return counts, nil
}
