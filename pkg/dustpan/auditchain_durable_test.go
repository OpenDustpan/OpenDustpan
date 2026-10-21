package dustpan_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// auditChainKind names the durable operation of the migration.
const auditChainKind durable.Kind = "audit-chain-migration"

// postgresURLEnv names the variable with a PostgreSQL URL for the
// PostgreSQL cases. Without it, the PostgreSQL cases skip.
const postgresURLEnv = "DUSTPAN_TEST_POSTGRES_URL"

// migrationOps answers the migration operations of st.
func migrationOps(t *testing.T, st *sqlite.Store) []durable.Operation {
	t.Helper()

	ops, err := st.DurableOperations().List(t.Context(), durable.Filter{
		Kind:   auditChainKind,
		States: nil,
		Limit:  0,
	})
	if err != nil {
		t.Fatalf("list migration operations: %v", err)
	}

	return ops
}

// migrationOpsPostgres answers the migration operations of st.
func migrationOpsPostgres(t *testing.T, st *postgres.Store) []durable.Operation {
	t.Helper()

	ops, err := st.DurableOperationsPort().List(t.Context(), durable.Filter{
		Kind:   auditChainKind,
		States: nil,
		Limit:  0,
	})
	if err != nil {
		t.Fatalf("list migration operations: %v", err)
	}

	return ops
}

// TestApprovalStartsOneMigrationOperation proves that an approval starts
// one durable operation, and that a second approval while it runs answers
// conflict and starts nothing more.
func TestApprovalStartsOneMigrationOperation(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		checkApprovalStartsOneSQLite(t)
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		checkApprovalStartsOnePostgres(t)
	})
}

// checkApprovalStartsOneSQLite runs the approval case on SQLite.
func checkApprovalStartsOneSQLite(t *testing.T) {
	t.Helper()

	st := openChainStore(t)
	ns := createChainNamespace(t, st)
	appendChainEntry(t, st, ns, "req-1")

	srv := runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	if status, body := srv.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusAccepted {
		t.Fatalf("approve: %d %s, want 202", status, body)
	}

	if got := migrationOps(t, st); len(got) != 1 {
		t.Fatalf("migration operations after the approval = %d, want 1", len(got))
	}

	if status, _ := srv.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusConflict {
		t.Fatalf("second approval: %d, want 409", status)
	}

	if got := migrationOps(t, st); len(got) != 1 {
		t.Fatalf("migration operations after two approvals = %d, want 1", len(got))
	}

	waitForRecord(t, st, func(rec audit.ChainRecord) bool {
		return rec.State == audit.ChainStateOff && !rec.Pending
	})
	waitForMaintenance(t, st, maintenance.StateNormal)
	srv.stop()
}

// checkApprovalStartsOnePostgres runs the approval case on PostgreSQL,
// skipping without a URL.
func checkApprovalStartsOnePostgres(t *testing.T) {
	t.Helper()

	base := os.Getenv(postgresURLEnv)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL case", postgresURLEnv)
	}

	st := openPostgresChainStore(t, base)
	ns := createPostgresNamespace(t, st)
	appendPostgresEntry(t, st, ns, "req-1")

	srv := runPostgresChainServer(t, st)
	waitForPostgresRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	if status, body := srv.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusAccepted {
		t.Fatalf("approve: %d %s, want 202", status, body)
	}

	if got := migrationOpsPostgres(t, st); len(got) != 1 {
		t.Fatalf("migration operations after the approval = %d, want 1", len(got))
	}

	if status, _ := srv.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusConflict {
		t.Fatalf("second approval: %d, want 409", status)
	}

	if got := migrationOpsPostgres(t, st); len(got) != 1 {
		t.Fatalf("migration operations after two approvals = %d, want 1", len(got))
	}

	waitForPostgresRecord(t, st, func(rec audit.ChainRecord) bool {
		return rec.State == audit.ChainStateOff && !rec.Pending
	})
	waitForPostgresMaintenance(t, st, maintenance.StateNormal)
	srv.stop()
}

// TestTwoStartsWithOneKeyGiveOneOperation proves the start key of the
// migration: two starts with one key give one operation.
func TestTwoStartsWithOneKeyGiveOneOperation(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		checkTwoStartsGiveOneSQLite(t)
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		checkTwoStartsGiveOnePostgres(t)
	})
}

// checkTwoStartsGiveOneSQLite runs the start-key case on SQLite.
func checkTwoStartsGiveOneSQLite(t *testing.T) {
	t.Helper()

	st := openChainStore(t)
	ops := st.DurableOperations()

	req := durable.StartRequest{Key: "audit-chain-migration", Kind: auditChainKind, Version: 1, Input: nil}

	first, created, err := ops.Start(t.Context(), req)
	if err != nil || !created {
		t.Fatalf("first start = %+v, %v, %v; want a new operation", first, created, err)
	}

	second, created, err := ops.Start(t.Context(), req)
	if err != nil || created {
		t.Fatalf("second start = %+v, %v, %v; want the first operation", second, created, err)
	}

	if first.ID != second.ID {
		t.Fatalf("two starts gave %s and %s, want one operation", first.ID, second.ID)
	}
}

// checkTwoStartsGiveOnePostgres runs the start-key case on PostgreSQL,
// skipping without a URL.
func checkTwoStartsGiveOnePostgres(t *testing.T) {
	t.Helper()

	base := os.Getenv(postgresURLEnv)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL case", postgresURLEnv)
	}

	st := openPostgresChainStore(t, base)
	ops := st.DurableOperationsPort()

	req := durable.StartRequest{Key: "audit-chain-migration", Kind: auditChainKind, Version: 1, Input: nil}

	first, created, err := ops.Start(t.Context(), req)
	if err != nil || !created {
		t.Fatalf("first start = %+v, %v, %v; want a new operation", first, created, err)
	}

	second, created, err := ops.Start(t.Context(), req)
	if err != nil || created {
		t.Fatalf("second start = %+v, %v, %v; want the first operation", second, created, err)
	}

	if first.ID != second.ID {
		t.Fatalf("two starts gave %s and %s, want one operation", first.ID, second.ID)
	}
}

// isolatedPostgresURL answers a URL for a fresh schema of the database at
// base, so each case starts empty.
func isolatedPostgresURL(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		t.Fatalf("create schema: %v", execErr)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", postgresURLEnv, err)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}

// openPostgresChainStore opens a fresh PostgreSQL store that the test closes.
func openPostgresChainStore(t *testing.T, base string) *postgres.Store {
	t.Helper()

	st, err := postgres.Open(t.Context(), isolatedPostgresURL(t, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.Close)

	return st
}

// createPostgresNamespace creates the namespace acme in st.
func createPostgresNamespace(t *testing.T, st *postgres.Store) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       time.Now(),
		Name:            "acme",
		HomeRegion:      region,
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return ns
}

// appendPostgresEntry appends one reveal entry to ns and answers it as stored.
func appendPostgresEntry(t *testing.T, st *postgres.Store, ns namespace.Namespace, requestID string) audit.Entry {
	t.Helper()

	stored, err := st.Append(t.Context(), ns.Fence(), audit.Entry{
		At:            time.Now(),
		Namespace:     ns.Name,
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "unit:read-secrets",
		Object:        "opentofu:acme/prod",
		ObjectID:      "",
		Version:       "sv-1",
		ValuePath:     "/outputs/db",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindReveal,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     requestID,
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	})
	if err != nil {
		t.Fatalf("append audit entry: %v", err)
	}

	return stored
}

// runPostgresChainServer starts a server on st with the chain off, waits
// until it serves, and answers it. The test stops it at its end.
func runPostgresChainServer(t *testing.T, st *postgres.Store) chainServer {
	t.Helper()

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	ln := openTestListener(t)
	addr := ln.Addr().String()

	registerConsole(t, addr, st)

	srv, err := dustpan.NewServer(append(signInOptions(t, st),
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(testWrapper(t)),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithListener(ln),
		dustpan.WithAuditChain(audit.ChainOff),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := runServer(ctx, srv)

	stopped := false

	stop := func() {
		if stopped {
			return
		}

		stopped = true

		cancel()

		if runErr := <-done; runErr != nil {
			t.Errorf("run: %v", runErr)
		}
	}

	t.Cleanup(stop)

	c := chainServer{srv: srv, stop: stop, base: "http://" + addr}
	waitForLive(t, c)

	return c
}

// waitForPostgresRecord waits until the chain record of st meets ok.
func waitForPostgresRecord(t *testing.T, st *postgres.Store, ok func(audit.ChainRecord) bool) {
	t.Helper()

	deadline := time.Now().Add(chainWait)

	for {
		rec, err := st.ChainRecord(t.Context())
		if err != nil {
			t.Fatalf("chain record: %v", err)
		}

		if ok(rec) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("chain record after %s: got %+v", chainWait, rec)
		}

		pauseChain(t)
	}
}

// waitForPostgresMaintenance waits until st holds the installation state want.
func waitForPostgresMaintenance(t *testing.T, st *postgres.Store, want maintenance.State) {
	t.Helper()

	deadline := time.Now().Add(chainWait)

	for {
		got, err := st.GetState(t.Context())
		if err == nil && got == want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("installation state after %s = %q, %v; want %q", chainWait, got, err, want)
		}

		pauseChain(t)
	}
}

// TestRunnerTakeoverCompletesTheMigration proves that when one instance
// stops during the migration, another instance completes it: the first
// runner blocks in its step, its context ends, and the second runner runs
// the step once and ends the operation succeeded.
func TestRunnerTakeoverCompletesTheMigration(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	store := st.DurableOperations()

	const kind durable.Kind = "takeover-kind"

	op := startTakeoverOp(t, store, kind)
	stopBlocking := runBlockingRunner(t, store, kind)
	stopBlocking()

	runs := runTakeoverRunner(t, store, kind)
	waitTakeoverSucceeded(t, store, op.ID)

	if runs() != 1 {
		t.Fatalf("second runner ran the step %d times, want 1", runs())
	}
}

// startTakeoverOp starts the takeover operation of kind.
func startTakeoverOp(t *testing.T, store durable.Store, kind durable.Kind) durable.Operation {
	t.Helper()

	op, _, err := store.Start(t.Context(), durable.StartRequest{Key: "k", Kind: kind, Version: 1, Input: nil})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	return op
}

// takeoverSteps answers the kind spec of kind with run as its step.
func takeoverSteps(kind durable.Kind, run durable.StepFunc) []durable.KindSpec {
	return []durable.KindSpec{{
		Kind:       kind,
		Version:    1,
		MinVersion: 1,
		Steps:      []durable.Step{{Name: "migrate", Run: run}},
	}}
}

// takeoverRunnerConfig answers the runner config of owner with kinds.
func takeoverRunnerConfig(store durable.Store, owner string, kinds []durable.KindSpec) durable.RunnerConfig {
	return durable.RunnerConfig{
		Store:        store,
		Owner:        owner,
		Kinds:        kinds,
		Lease:        300 * time.Millisecond,
		RenewEvery:   0,
		PollEvery:    0,
		MaxAttempts:  0,
		ExpiryLimit:  0,
		Backoff:      durable.Backoff{Base: 0, Max: 0},
		ClaimBackoff: durable.Backoff{Base: 0, Max: 0},
		WriteTimeout: 0,
		Deferred:     nil,
		Logger:       nil,
		Rand:         nil,
	}
}

// runBlockingRunner runs the first runner until its step blocks, and
// answers the stop function that ends it and waits.
func runBlockingRunner(t *testing.T, store durable.Store, kind durable.Kind) func() {
	t.Helper()

	started := make(chan struct{})
	var once sync.Once

	block := func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()

		return nil, ctx.Err()
	}

	first, err := durable.NewRunner(takeoverRunnerConfig(store, "first", takeoverSteps(kind, block)))
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	ctx, stop := context.WithCancel(t.Context())

	var wg sync.WaitGroup

	wg.Go(func() { first.Run(ctx) })

	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("test context ended")
	}

	return func() {
		stop()
		wg.Wait()
	}
}

// runTakeoverRunner runs the second runner once, and answers the count of
// its step runs.
func runTakeoverRunner(t *testing.T, store durable.Store, kind durable.Kind) func() int {
	t.Helper()

	var runs int

	second, err := durable.NewRunner(takeoverRunnerConfig(store, "second", takeoverSteps(kind,
		func(context.Context, durable.StepContext) ([]byte, error) {
			runs++

			return []byte("done"), nil
		})))
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	if err := second.RunOnce(t.Context()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	return func() int { return runs }
}

// waitTakeoverSucceeded waits until the operation id ends succeeded.
func waitTakeoverSucceeded(t *testing.T, store durable.Store, id durable.ID) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for {
		got, err := store.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}

		if got.State == durable.StateSucceeded {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("operation = %+v, want succeeded", got)
		}

		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("test context ended")
		case <-timer.C:
		}
	}
}
