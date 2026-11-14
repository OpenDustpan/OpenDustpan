package sqlite //nolint:testpackage // The white-box template state is unexported, so the exporter must stay in that package.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// DB returns the database handle of s, so a test can read or change rows
// with direct SQL.
func DB(s *Store) *sql.DB {
	return s.conn
}

// RemoveInternalTemplates removes the white-box template directory built for
// this test binary. It skips removal when the template was never built.
func RemoveInternalTemplates() error {
	if internalTemplateState.dir == "" {
		return nil
	}

	if err := os.RemoveAll(internalTemplateState.dir); err != nil {
		return fmt.Errorf("remove store template: %w", err)
	}

	return nil
}

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

// SetBetweenPageReads sets the hook that QueryStacks and QueryUnits run after
// the statement of the page and before the statement of its tags or labels.
func SetBetweenPageReads(s *Store, hook func(ctx context.Context)) {
	s.betweenPageReads = hook
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
		VALUES ('control-session', 'op-control', 1, 'rn-control', X'00', 1, 1)`,
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
		if _, err := s.conn.ExecContext(ctx, stmt); err != nil {
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
		VALUES (?, ?, ?)
		ON CONFLICT (owner) DO UPDATE SET at_ms = excluded.at_ms, lease_ms = excluded.lease_ms`

	if _, err := s.conn.ExecContext(ctx, stmt, owner, at.UnixMilli(), lease.Milliseconds()); err != nil {
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
		if err := s.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
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

// JobWrite runs fn in one write transaction of the job stores of s.
func JobWrite(ctx context.Context, s *Store, fence namespace.Fence, fn func(jobstore.Tx) error) error {
	return jobRunner{s: s}.Write(ctx, fence, fn)
}

// JobRead runs fn in one read transaction of the job stores of s.
func JobRead(ctx context.Context, s *Store, fn func(jobstore.Tx) error) error {
	return jobRunner{s: s}.Read(ctx, fn)
}
