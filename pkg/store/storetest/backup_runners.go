package storetest

import (
	"bytes"
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
// their join tokens, and the members that hold the runners and their keys.
type RunnerPorts struct {
	Pools   runner.PoolStore
	Members runner.MemberStore
}

// RunnerSeeder is an optional BackupStore capability: a store that keeps
// runner pools gives its ports, so the round trip fills and compares the
// three runner tables. A store with no runner port skips them: the fake of
// this package's own tests is one.
type RunnerSeeder interface {
	RunnerPorts() RunnerPorts
}

// runnerTables returns the runner tables in backup.Tables.
func runnerTables() []string {
	return []string{"runner_pools", "runner_join_tokens", "runners"}
}

// runnerControlTables returns the tables of the runner protocol that the
// backup does not hold. A restore leaves them empty.
func runnerControlTables() []string {
	return []string{"runner_claims", "runner_handoff_tokens", "runner_sessions", "runner_nonces"}
}

// seededRunners names what seedBackupRunners wrote.
type seededRunners struct {
	pool     runner.Pool
	token    runner.JoinToken
	runner   runner.Runner
	key      runner.Key
	secret   string
	joinedAt time.Time
}

// runnerSecret is the join secret of the seeded token. The store keeps only
// its hash, so the round trip proves that the hash travels byte for byte.
const runnerSecret = runner.JoinTokenPrefix + "seed-secret-0123456789"

// seedBackupRunners writes one pool in acme, one join token of the pool, and
// one runner that joined with that token. It returns nil for a store with no
// runner port.
func seedBackupRunners(t *testing.T, s BackupStore, acme namespace.Namespace) *seededRunners {
	t.Helper()

	seeder, ok := s.(RunnerSeeder)
	if !ok {
		return nil
	}

	ports := seeder.RunnerPorts()
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

	sum := hashSecret(runnerSecret)

	token, err := ports.Pools.CreateJoinToken(ctx, runner.JoinToken{
		ID: "", Pool: pool.ID, SHA256: sum, CreatedBy: "alice", CreatedAt: now(),
		ExpiresAt: now().Add(time.Hour), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
	}, fence, entries)
	if err != nil {
		t.Fatalf("create join token: %v", err)
	}

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate runner key: %v", err)
	}

	key := runner.Key{Public: pub, CreatedAt: time.Time{}}

	joinedAt := now()

	joined, err := ports.Members.Join(ctx, runner.Joining{
		TokenSHA256: sum,
		Request: runner.JoinRequest{
			Token: "", Name: "linux-1", Kind: runner.KindKubernetes, Isolation: runner.IsolationKubernetes,
			Labels:  map[string]string{"region": "eu-west-1"},
			Release: runner.Release{Major: 0, Minor: 14, Patch: 0, Pre: ""},
			Key:     key, Proof: nil,
		},
		At: joinedAt,
	}, fence, func(j *runner.Joined) []audit.Entry {
		e := lifecycleEntry(acme.Name, string(runner.ActionRunnerJoin))
		e.ObjectID = j.Runner.ID

		return []audit.Entry{e}
	})
	if err != nil {
		t.Fatalf("join runner: %v", err)
	}

	return &seededRunners{
		pool: pool, token: token, runner: joined.Runner, key: key, secret: runnerSecret, joinedAt: joinedAt,
	}
}

// hashSecret returns the hex SHA-256 of a join secret, as the stores keep it.
func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))

	return hex.EncodeToString(sum[:])
}

// checkRunnerRowCounts checks that sum holds one row in each runner table,
// and no row of a control table, when the store keeps runner pools. The
// control tables are not in the backup, so the summary has no count for them.
func checkRunnerRowCounts(t *testing.T, s BackupStore, sum backup.Summary) {
	t.Helper()

	if _, ok := s.(RunnerSeeder); !ok {
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
}

// checkRestoredRunners checks that dst holds the seeded pool, join token and
// runner after a restore, field by field, and that the key of the runner is
// the key that was seeded.
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

	checkRestoredJoinToken(t, seeded, tokens[0])

	got, err := ports.Members.Runner(ctx, seeded.runner.ID)
	if err != nil {
		t.Fatalf("restored runner %s: %v", seeded.runner.ID, err)
	}

	compareRunner(t, seeded, got)
	compareRunnerLabels(t, seeded.runner, got)
}

// checkRestoredJoinToken fails the test when tok is not the seeded token, used
// once by the seeded runner.
func checkRestoredJoinToken(t *testing.T, seeded *seededRunners, tok runner.JoinToken) {
	t.Helper()

	if tok.ID != seeded.token.ID || tok.SHA256 != hashSecret(seeded.secret) || tok.UsedBy != seeded.runner.ID ||
		!tok.UsedAt.Equal(seeded.joinedAt) || tok.CreatedBy != seeded.token.CreatedBy {
		t.Errorf("restored join token = %+v, want the seeded token used by runner %s", tok, seeded.runner.ID)
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

// compareRunner fails the test for each field of got that differs from the
// runner that was seeded.
func compareRunner(t *testing.T, seeded *seededRunners, got runner.Runner) {
	t.Helper()

	want := seeded.runner

	if got.ID != want.ID || got.Pool != want.Pool || got.Namespace != want.Namespace || got.Name != want.Name {
		t.Errorf("restored runner = %+v, want %+v", got, want)
	}

	if got.Kind != want.Kind || got.Isolation != want.Isolation || got.Release != want.Release {
		t.Errorf("restored runner kind, isolation, release = %v, %v, %+v, want %v, %v, %+v",
			got.Kind, got.Isolation, got.Release, want.Kind, want.Isolation, want.Release)
	}

	// The stores keep the hash of the join secret in JoinedWith. The port
	// doc calls it the token ID. The round trip checks the stored value.
	if got.JoinedWith != hashSecret(seeded.secret) || !got.JoinedAt.Equal(seeded.joinedAt) {
		t.Errorf("restored runner join = %q at %v, want %q at %v",
			got.JoinedWith, got.JoinedAt, hashSecret(seeded.secret), seeded.joinedAt)
	}

	if !bytes.Equal(got.Key.Public, seeded.key.Public) || !got.Key.CreatedAt.Equal(seeded.joinedAt) {
		t.Errorf("restored runner key = %x at %v, want %x at %v",
			got.Key.Public, got.Key.CreatedAt, seeded.key.Public, seeded.joinedAt)
	}
}

// compareRunnerLabels fails the test when the labels of got differ from the
// labels of the seeded runner.
func compareRunnerLabels(t *testing.T, want, got runner.Runner) {
	t.Helper()

	if !maps.Equal(got.Labels, want.Labels) {
		t.Errorf("restored runner labels = %v, want %v", got.Labels, want.Labels)
	}
}
