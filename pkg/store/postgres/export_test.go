package postgres //nolint:testpackage // The unexported write path of the setting must be reachable without its pre-check, so the exporter must stay in that package.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// Pool returns the connection pool of s, so a test can read or change rows
// with direct SQL.
func Pool(s *Store) *pgxpool.Pool {
	return s.pool
}

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
			stmt: `INSERT INTO runner_handoff_tokens (sha256, operation_id, token, runner_id, kind, issued_at_ms, ` +
				`expires_at_ms, used_at_ms) VALUES ('control-hash', 'op-control', 1, $1, 'launcher', 1, 2, 0)`,
			args: []any{runnerID},
		},
		{
			stmt: `INSERT INTO runner_sessions (id, operation_id, token, runner_id, kind, key_public, ` +
				`key_created_at_ms, created_at_ms) VALUES ('control-session', 'op-control', 1, $1, 'launcher', ` +
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

// SetItemKeyAttemptDeadline makes the attempt deadline of each item key
// transaction that starts after the call d after its start, for the late
// commit cases of the conformance suite.
func SetItemKeyAttemptDeadline(s *Store, d time.Duration) {
	s.attemptDeadline = func(_ context.Context, start time.Time) time.Time {
		return start.Add(d)
	}
}

// SetNowForTest replaces the clock of the item key attempt code of s.
func SetNowForTest(s *Store, now func() time.Time) {
	s.now = now
}

// JobWrite runs fn in one write transaction of the job stores of s.
func JobWrite(ctx context.Context, s *Store, fence namespace.Fence, fn func(jobstore.Tx) error) error {
	return jobRunner{s: s}.Write(ctx, fence, fn)
}

// JobRead runs fn in one read transaction of the job stores of s.
func JobRead(ctx context.Context, s *Store, fn func(jobstore.Tx) error) error {
	return jobRunner{s: s}.Read(ctx, fn)
}

// MarkBreakGlassForTest sets the break-glass flag of the principal
// principalID with direct SQL, as a restored row of a backup can hold it. No
// port sets the flag on a principal that exists.
func (s *Store) MarkBreakGlassForTest(t *testing.T, principalID string) {
	t.Helper()

	tag, err := s.pool.Exec(t.Context(), `UPDATE principals SET break_glass = true WHERE id = $1`, principalID)
	if err != nil {
		t.Fatalf("mark %s as a break-glass account: %v", principalID, err)
	}

	if tag.RowsAffected() != 1 {
		t.Fatalf("mark %s as a break-glass account: %d rows, want 1 row", principalID, tag.RowsAffected())
	}
}
