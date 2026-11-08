package postgres //nolint:testpackage // The unexported write path of the setting must be reachable without its pre-check, so the exporter must stay in that package.

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// CheckSettingFence runs the comparison of the fence and the namespace that
// SetSetting makes before it opens a transaction.
func CheckSettingFence(ns namespace.Name, fence namespace.Fence) error {
	return settingFenceBinds(ns, fence)
}

// MarkSupersededCheckpointInTx runs the mark of the replaced checkpoint in its
// own transaction. cp names the write, and readKey is the key that the sequence
// check read.
func MarkSupersededCheckpointInTx(
	ctx context.Context, s *Store, stackID stack.ID, cp stack.Checkpoint, readKey blob.Key,
) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		return markSuperseded(ctx, q, cp, stackID.String(), readKey)
	})
}

// AgeSupersededMarks moves every mark of the stack before at, with a direct
// SQL update. The cases of the superseded sweep need marks older than the
// grace period without a wait.
func (s *Store) AgeSupersededMarks(ctx context.Context, stackID stack.ID, at time.Time) error {
	const stmt = `UPDATE checkpoint_supersedes SET superseded_at_ms = $1 WHERE stack_id = $2`

	if _, err := s.pool.Exec(ctx, stmt, at.UnixMilli(), stackID.String()); err != nil {
		return fmt.Errorf("age superseded marks: %w", err)
	}

	return nil
}

// SetAfterSequenceCheck sets the hook that sequenceWrite runs after
// checkSequence returns and before the mark.
func SetAfterSequenceCheck(s *Store, hook func(stackID string)) {
	s.afterSequenceCheck = hook
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

// runnerControlStatements write one row in each control table of the runner
// protocol. The control rows of a live session need a durable operation, so
// the test writes them through SQL.
//
//nolint:gochecknoglobals // Fixed SQL of the test hook, read only.
var runnerControlStatements = []string{
	`INSERT INTO runner_claims (operation_id, token, claimed_at_ms, job_id, phase) VALUES ('op-control', 1, 1, '', 0)`,
	`INSERT INTO runner_handoff_tokens (sha256, operation_id, token, runner_id, issued_at_ms, expires_at_ms, used_at_ms)
		VALUES ('control-hash', 'op-control', 1, 'rn-control', 1, 2, 0)`,
	`INSERT INTO runner_sessions (id, operation_id, token, runner_id, key_public, key_created_at_ms, created_at_ms)
		VALUES ('control-session', 'op-control', 1, 'rn-control', decode('00', 'hex'), 1, 1)`,
	`INSERT INTO runner_nonces (subject, nonce, expires_at_ms) VALUES ('control', 'nonce', 2)`,
}

// runnerControlTableNames lists the control tables that SeedRunnerControlRows
// writes and CountRunnerControlRows counts.
//
//nolint:gochecknoglobals // Fixed table names of the test hook, read only.
var runnerControlTableNames = []string{"runner_claims", "runner_handoff_tokens", "runner_sessions", "runner_nonces"}

// SeedRunnerControlRows writes one row in each control table of the runner
// protocol.
func SeedRunnerControlRows(ctx context.Context, s *Store) error {
	for _, stmt := range runnerControlStatements {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("seed runner control row: %w", err)
		}
	}

	return nil
}

// SeedHeartbeatRow writes one durable_heartbeats row with exactly the given
// time and lease, as the upsert of Beat writes it. It is a test hook for the
// ordering cases of the heartbeat suite, which cannot wait on the clock of
// the store.
func SeedHeartbeatRow(ctx context.Context, s *Store, owner string, at time.Time, lease time.Duration) error {
	const stmt = `INSERT INTO durable_heartbeats (owner, at_ms, lease_ms)
		VALUES ($1, $2, $3)
		ON CONFLICT (owner) DO UPDATE SET at_ms = excluded.at_ms, lease_ms = excluded.lease_ms`

	if _, err := s.pool.Exec(ctx, stmt, owner, at.UnixMilli(), lease.Milliseconds()); err != nil {
		return fmt.Errorf("seed heartbeat row: %w", err)
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
