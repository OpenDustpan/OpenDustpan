package storetest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

// testRestoreKeepsRunnerControlRows proves that a restore keeps the live
// control rows of the runner protocol in the destination: a live claim,
// handoff token, job session and nonce are still there after the swap. The
// rows describe claims of the live durable operations, so a restore that drops
// them leaves a runner with a claim that the server no longer knows. The
// control rows name the runner of the restored data, so the copy keeps them.
func testRestoreKeepsRunnerControlRows(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	src := newStore(t)
	dst := newStore(t)

	seeder, ok := dst.(RunnerSeeder)
	if !ok {
		t.Skip("the store keeps no runner control rows")
	}

	ps := portableRestoreStores(t, src, dst)
	ctx := t.Context()

	seed := seedBackupStore(t, ps.src)
	if seed.runners == nil {
		t.Skip("the source store keeps no runner rows")
	}

	if err := seeder.SeedRunnerControlRows(ctx, seed.runners.runner.ID); err != nil {
		t.Fatalf("seed the live runner control rows: %v", err)
	}

	sink, sum, files := exportPortable(t, ps.src)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	counts, err := seeder.CountRunnerControlRows(ctx)
	if err != nil {
		t.Fatalf("count runner control rows after the restore: %v", err)
	}

	for _, table := range runnerControlTables() {
		if counts[table] != 1 {
			t.Errorf("rows of control table %q after the restore = %d, want the 1 live row", table, counts[table])
		}
	}
}

// testRestoreDropsRunnerCredentials proves that a restore drops a live
// handoff token and a live job session whose runner the restored data does
// not hold: the backup predates the runner, so a later use of either
// credential fails as unknown, never with a store error.
func testRestoreDropsRunnerCredentials(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	src := newStore(t)
	dst := newStore(t)

	seeder, ok := dst.(RunnerSeeder)
	if !ok {
		t.Skip("the store keeps no runner credentials")
	}

	ps := portableRestoreStores(t, src, dst)
	ctx := t.Context()

	// The backup predates the runner.
	seedBackupStore(t, ps.src)
	sink, sum, files := exportPortable(t, ps.src)

	// The live installation registers a runner after the backup and issues
	// a handoff token and a job session for it.
	acme := newNamespace(t, ps.dst, "acme")
	ports := seeder.RunnerPorts()
	seeded := joinBackupRunner(t, ports, acme, "linux-1")
	credentials := issueRunnerCredentials(t, ps.dst, ports, seeded.pool, seeded.runner)

	if _, err := ps.dst.StagePortable(ctx, sink.read, sum, files, StageAs(t, ps.dst)); err != nil {
		t.Fatalf("stage portable backup: %v", err)
	}

	if err := ps.dst.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staged backup: %v", err)
	}

	if _, err := ports.Sessions.OpenSession(ctx, credentials.token.SHA256, newRunnerKey(t), now()); !errors.Is(
		err, runner.ErrNotFound,
	) {
		t.Errorf("OpenSession with the handoff token of a runner that the restored data does not hold = %v, "+
			"want runner.ErrNotFound", err)
	}

	if _, err := ports.Sessions.Session(ctx, credentials.session.ID); !errors.Is(err, runner.ErrNotFound) {
		t.Errorf("Session of a runner that the restored data does not hold = %v, want runner.ErrNotFound", err)
	}
}

// runnerCredentials is the handoff token and the job session that the token
// opened for one live claim of a runner.
type runnerCredentials struct {
	token   runner.HandoffToken
	session runner.Session
}

// issueRunnerCredentials claims one operation of pool for the runner r,
// issues a handoff token for the claim and opens the job session of the
// token, through the ports.
func issueRunnerCredentials(
	t *testing.T, s RestoreStore, ports RunnerPorts, pool runner.Pool, r runner.Runner,
) runnerCredentials {
	t.Helper()

	ctx := t.Context()
	ops := s.DurableOperationsPort()
	kind := job.RunnerKind(pool.ID)

	start := durable.StartRequest{Key: "restore-runner-credentials", Kind: kind, Version: 0, Input: nil}
	if _, _, err := ops.Start(ctx, start); err != nil {
		t.Fatalf("start the durable operation of the runner credentials: %v", err)
	}

	claimed, err := ops.Claim(ctx, durable.ClaimRequest{
		Owner: r.ID, Kinds: []durable.Kind{kind}, MaxVersions: nil, Lease: time.Hour, TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim the durable operation of the runner credentials: %v", err)
	}

	digest := sha256.Sum256([]byte(runnerHandoffSecret))
	token := runner.HandoffToken{
		SHA256: hex.EncodeToString(digest[:]), Operation: string(claimed.ID), Token: claimed.Claim.Token,
		Runner: r.ID, IssuedAt: now(), ExpiresAt: now().Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}

	if issueErr := ports.Sessions.IssueHandoffToken(ctx, token, claimed.Claim); issueErr != nil {
		t.Fatalf("issue the handoff token of the runner credentials: %v", issueErr)
	}

	session, err := ports.Sessions.OpenSession(ctx, token.SHA256, newRunnerKey(t), now())
	if err != nil {
		t.Fatalf("open the job session of the runner credentials: %v", err)
	}

	return runnerCredentials{token: token, session: session}
}

// newRunnerKey makes a new Ed25519 public key for a job session.
func newRunnerKey(t *testing.T) runner.Key {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate a runner key: %v", err)
	}

	return runner.Key{Public: pub, CreatedAt: time.Time{}}
}

// runnerHandoffSecret is the secret of the handoff token of the restore
// cases. The store keeps only its hash.
const runnerHandoffSecret = runner.HandoffTokenPrefix + "restore-secret-0123456789"
