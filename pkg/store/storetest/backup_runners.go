package storetest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// RunnerPorts are the ports of the runner tool on one store: the pools and
// their join tokens, the members that hold the runners and their keys, and
// the handoff tokens and job sessions of the runners.
type RunnerPorts struct {
	Pools    runner.PoolStore
	Members  runner.MemberStore
	Sessions runner.SessionStore
}

// RunnerSeeder is a BackupStore capability for a store that keeps runner
// pools. The round trip fills and compares the three runner tables through
// RunnerPorts. The control rows of the runner protocol are not in the backup:
// SeedRunnerControlRows writes one row in each of them, for the runner
// runnerID in the rows that name one, and CountRunnerControlRows reads the
// rows of each. A store that has no runner port does not implement this
// interface, so the suite refuses it at compile time.
type RunnerSeeder interface {
	RunnerPorts() RunnerPorts
	// SeedRunnerControlRows writes one row in each control table. The rows
	// that name a runner name runnerID.
	SeedRunnerControlRows(ctx context.Context, runnerID string) error
	// CountRunnerControlRows returns the rows that each control table holds.
	CountRunnerControlRows(ctx context.Context) (map[string]int64, error)
}

// runnerTables returns the runner tables in backup.Tables.
func runnerTables() []string {
	return []string{"runner_pools", "runner_join_tokens", "runners"}
}

// runnerControlTables returns the tables of the runner protocol that the
// backup does not hold. A restore does not carry their rows.
func runnerControlTables() []string {
	return []string{"runner_claims", "runner_handoff_tokens", "runner_sessions", "runner_nonces"}
}

// seededRunners holds the pool, the join token and the runner as the store
// returned them after the join, read back before the backup. The round trip
// compares the restored rows with these values.
type seededRunners struct {
	pool   runner.Pool
	token  runner.JoinToken
	runner runner.Runner
}

// runnerSecret is the join secret of the seeded token. The store keeps only
// its hash.
const runnerSecret = runner.JoinTokenPrefix + "seed-secret-0123456789"

// seedBackupRunners writes one pool in acme, one join token of the pool, and
// one runner that joined with that token. It also writes one row in each
// control table of the runner protocol, for that runner in the rows that name
// one, so that the restore check can prove that those rows do not arrive. It
// returns nil for a store with no runner port.
func seedBackupRunners(t *testing.T, s BackupStore, acme namespace.Namespace) *seededRunners {
	t.Helper()

	seeder, ok := s.(RunnerSeeder)
	if !ok {
		return nil
	}

	seeded := joinBackupRunner(t, seeder.RunnerPorts(), acme, "linux-1")

	if err := seeder.SeedRunnerControlRows(t.Context(), seeded.runner.ID); err != nil {
		t.Fatalf("seed runner control rows: %v", err)
	}

	return seeded
}

// joinBackupRunner creates one pool in acme, one join token of the pool, and
// one runner called name that joins with the secret of the token, through the
// ports. It returns the pool, the token and the runner as the store returns
// them after the join.
func joinBackupRunner(t *testing.T, ports RunnerPorts, acme namespace.Namespace, name string) *seededRunners {
	t.Helper()

	ctx := t.Context()
	fence := acme.Fence()
	entries := []audit.Entry{lifecycleEntry(acme.Name, "namespace:manage-runners")}

	pool, err := ports.Pools.CreatePool(ctx, runner.Pool{
		ID: "", Namespace: acme.Name, Name: "builds", Purpose: runner.PurposeJobs,
		Lease: runner.DefaultLease, Concurrency: 2, Isolation: []runner.Isolation{runner.IsolationKubernetes},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsForge, Helper: ""},
		CreatedAt:      now(), UpdatedAt: now(),
	}, fence, entries)
	if err != nil {
		t.Fatalf("create runner pool: %v", err)
	}

	digest := sha256.Sum256([]byte(runnerSecret))
	tokenHash := hex.EncodeToString(digest[:])

	token, err := ports.Pools.CreateJoinToken(ctx, runner.JoinToken{
		ID: "", Pool: pool.ID, SHA256: tokenHash, CreatedBy: "alice", CreatedAt: now(),
		ExpiresAt: now().Add(time.Hour), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
	}, fence, entries)
	if err != nil {
		t.Fatalf("create join token: %v", err)
	}

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate runner key: %v", err)
	}

	joined, err := ports.Members.Join(ctx, runner.Joining{
		TokenSHA256: token.SHA256,
		Request: runner.JoinRequest{
			Token: "", Name: name, Kind: runner.KindKubernetes, Isolation: runner.IsolationKubernetes,
			Labels:  map[string]string{"region": "eu-west-1"},
			Release: runner.Release{Major: 0, Minor: 14, Patch: 0, Pre: ""},
			Key:     runner.Key{Public: pub, CreatedAt: time.Time{}}, Proof: nil,
		},
		At: now(),
	}, fence, func(j *runner.Joined) []audit.Entry {
		e := lifecycleEntry(acme.Name, string(runner.ActionRunnerJoin))
		e.ObjectID = j.Runner.ID

		return []audit.Entry{e}
	})
	if err != nil {
		t.Fatalf("join runner: %v", err)
	}

	return &seededRunners{
		pool:   readBackPool(t, ports, acme, pool.ID),
		token:  readBackJoinToken(t, ports, pool.ID),
		runner: readBackRunner(t, ports, joined.Runner.ID),
	}
}

// readBackPool returns the pool id as the store returns it now.
func readBackPool(t *testing.T, ports RunnerPorts, acme namespace.Namespace, id string) runner.Pool {
	t.Helper()

	pool, err := ports.Pools.Pool(t.Context(), id)
	if err != nil {
		t.Fatalf("read runner pool %s: %v", id, err)
	}

	if pool.Namespace != acme.Name {
		t.Fatalf("runner pool namespace = %q, want %q", pool.Namespace, acme.Name)
	}

	return pool
}

// readBackJoinToken returns the one join token of the pool as the store
// returns it now.
func readBackJoinToken(t *testing.T, ports RunnerPorts, pool string) runner.JoinToken {
	t.Helper()

	tokens, err := ports.Pools.JoinTokens(t.Context(), pool)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("read join tokens of pool %s = %+v, %v, want one token", pool, tokens, err)
	}

	return tokens[0]
}

// readBackRunner returns the runner id as the store returns it now.
func readBackRunner(t *testing.T, ports RunnerPorts, id string) runner.Runner {
	t.Helper()

	r, err := ports.Members.Runner(t.Context(), id)
	if err != nil {
		t.Fatalf("read runner %s: %v", id, err)
	}

	return r
}

// checkRunnerRowCounts checks that sum holds one row in each runner table
// when the store keeps runner pools, and that the source holds one row in
// each control table. The control tables are not in the backup, so the
// summary has no count for them.
func checkRunnerRowCounts(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	seeder, ok := s.(RunnerSeeder)
	if !ok {
		return
	}

	for _, table := range runnerTables() {
		if got := sum.RowCounts[table]; got != 1 {
			t.Errorf("exported rows of %q = %d, want 1", table, got)
		}
	}

	for _, table := range runnerControlTables() {
		if got, ok := sum.RowCounts[table]; ok {
			t.Errorf("exported rows of control table %q = %d, want none in the backup", table, got)
		}
	}

	counts, err := seeder.CountRunnerControlRows(t.Context())
	if err != nil {
		t.Fatalf("count runner control rows of the source: %v", err)
	}

	for _, table := range runnerControlTables() {
		if counts[table] != 1 {
			t.Errorf("source rows of control table %q = %d, want 1", table, counts[table])
		}
	}
}

// checkRestoredRunners checks that dst holds the seeded pool, join token and
// runner after a restore, field by field, as the source held them before the
// backup.
func checkRestoredRunners(t *testing.T, dst RestoreStore, acme namespace.Name, seeded *seededRunners) {
	t.Helper()

	seeder, ok := dst.(RunnerSeeder)
	if !ok || seeded == nil {
		return
	}

	ports := seeder.RunnerPorts()
	ctx := t.Context()

	pools, err := ports.Pools.Pools(ctx, acme)
	if err != nil || len(pools) != 1 {
		t.Fatalf("restored pools = %+v, %v, want the seeded pool", pools, err)
	}

	pool, err := ports.Pools.Pool(ctx, seeded.pool.ID)
	if err != nil {
		t.Fatalf("restored pool %s: %v", seeded.pool.ID, err)
	}

	comparePool(t, seeded.pool, pool)

	tokens, err := ports.Pools.JoinTokens(ctx, pool.ID)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("restored join tokens = %+v, %v, want the seeded token", tokens, err)
	}

	compareJoinToken(t, seeded.token, tokens[0])

	got, err := ports.Members.Runner(ctx, seeded.runner.ID)
	if err != nil {
		t.Fatalf("restored runner %s: %v", seeded.runner.ID, err)
	}

	compareRunner(t, seeded.runner, got)
}

// checkRunnerControlRowsAbsent checks that dst holds no row in any control
// table of the runner protocol. The restore does not carry the rows of the
// exported store, and dst was empty before the restore.
func checkRunnerControlRowsAbsent(t *testing.T, dst RestoreStore) {
	t.Helper()

	seeder, ok := dst.(RunnerSeeder)
	if !ok {
		return
	}

	counts, err := seeder.CountRunnerControlRows(t.Context())
	if err != nil {
		t.Fatalf("count runner control rows after the restore: %v", err)
	}

	for _, table := range runnerControlTables() {
		if counts[table] != 0 {
			t.Errorf("restored rows of control table %q = %d, want 0: the rows of the exported store must not arrive",
				table, counts[table])
		}
	}
}

// comparePool fails the test for each field of got that differs from want.
func comparePool(t *testing.T, want, got runner.Pool) {
	t.Helper()

	if got.ID != want.ID || got.Namespace != want.Namespace || got.Name != want.Name || got.Purpose != want.Purpose {
		t.Errorf("restored pool = %+v, want %+v", got, want)
	}

	if got.Lease != want.Lease || got.Concurrency != want.Concurrency {
		t.Errorf("restored pool lease, concurrency = %v, %d, want %v, %d",
			got.Lease, got.Concurrency, want.Lease, want.Concurrency)
	}

	if !slices.Equal(got.Isolation, want.Isolation) {
		t.Errorf("restored pool isolation = %v, want %v", got.Isolation, want.Isolation)
	}

	if got.GitCredentials != want.GitCredentials {
		t.Errorf("restored pool git credentials = %+v, want %+v", got.GitCredentials, want.GitCredentials)
	}

	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("restored pool times = %v, %v, want %v, %v",
			got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}
}

// compareJoinToken fails the test for each field of got that differs from
// want.
func compareJoinToken(t *testing.T, want, got runner.JoinToken) {
	t.Helper()

	if got.ID != want.ID || got.Pool != want.Pool || got.SHA256 != want.SHA256 ||
		got.CreatedBy != want.CreatedBy || got.UsedBy != want.UsedBy {
		t.Errorf("restored join token = %+v, want %+v", got, want)
	}

	if !got.CreatedAt.Equal(want.CreatedAt) || !got.ExpiresAt.Equal(want.ExpiresAt) ||
		!got.UsedAt.Equal(want.UsedAt) || !got.RevokedAt.Equal(want.RevokedAt) {
		t.Errorf("restored join token times = %v, %v, %v, %v, want %v, %v, %v, %v",
			got.CreatedAt, got.ExpiresAt, got.UsedAt, got.RevokedAt,
			want.CreatedAt, want.ExpiresAt, want.UsedAt, want.RevokedAt)
	}
}

// compareRunner fails the test for each field of got that differs from want,
// the runner as the store returned it before the backup.
func compareRunner(t *testing.T, want, got runner.Runner) {
	t.Helper()

	if got.ID != want.ID || got.Pool != want.Pool || got.Namespace != want.Namespace || got.Name != want.Name {
		t.Errorf("restored runner = %+v, want %+v", got, want)
	}

	if got.Kind != want.Kind || got.Isolation != want.Isolation || got.Release != want.Release {
		t.Errorf("restored runner kind, isolation, release = %v, %v, %+v, want %v, %v, %+v",
			got.Kind, got.Isolation, got.Release, want.Kind, want.Isolation, want.Release)
	}

	if got.JoinedWith != want.JoinedWith || got.Draining != want.Draining {
		t.Errorf("restored runner join, draining = %q, %t, want %q, %t",
			got.JoinedWith, got.Draining, want.JoinedWith, want.Draining)
	}

	compareRunnerKeys(t, want, got)
	compareRunnerTimes(t, want, got)

	if !maps.Equal(got.Labels, want.Labels) {
		t.Errorf("restored runner labels = %v, want %v", got.Labels, want.Labels)
	}
}

// compareRunnerKeys fails the test when a key of got differs from the key of
// want.
func compareRunnerKeys(t *testing.T, want, got runner.Runner) {
	t.Helper()

	if !bytes.Equal(got.Key.Public, want.Key.Public) || !got.Key.CreatedAt.Equal(want.Key.CreatedAt) {
		t.Errorf("restored runner key = %x at %v, want %x at %v",
			got.Key.Public, got.Key.CreatedAt, want.Key.Public, want.Key.CreatedAt)
	}

	if !bytes.Equal(got.PreviousKey.Public, want.PreviousKey.Public) {
		t.Errorf("restored runner previous key = %x, want %x", got.PreviousKey.Public, want.PreviousKey.Public)
	}
}

// compareRunnerTimes fails the test when a time of got differs from the time
// of want.
func compareRunnerTimes(t *testing.T, want, got runner.Runner) {
	t.Helper()

	if !got.JoinedAt.Equal(want.JoinedAt) || !got.LastSeenAt.Equal(want.LastSeenAt) ||
		!got.RotatedAt.Equal(want.RotatedAt) || !got.RetiredAt.Equal(want.RetiredAt) {
		t.Errorf("restored runner times = %v, %v, %v, %v, want %v, %v, %v, %v",
			got.JoinedAt, got.LastSeenAt, got.RotatedAt, got.RetiredAt,
			want.JoinedAt, want.LastSeenAt, want.RotatedAt, want.RetiredAt)
	}
}
