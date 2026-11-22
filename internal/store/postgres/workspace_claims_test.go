package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// TestClaimStoreConformance runs the claim conformance suite against
// PostgreSQL: the workspace row lock orders the parallel registrations.
func TestClaimStoreConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	workspacetest.RunClaimStore(t, func(t *testing.T) workspacetest.ClaimBackend {
		t.Helper()

		return open(t, base)
	})
}

// TestClaimWaitsForTheWorkspaceRowLock takes the row lock of the workspace in
// a second transaction and shows that WithClaims waits for it, and then runs
// when the lock goes. The row lock is what orders two registrations on one
// workspace on this adapter.
func TestClaimWaitsForTheWorkspaceRowLock(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	fence, ws := workspacetest.SeedClaimWorkspace(t, s)

	tx := holdWorkspaceRowLock(t, schemaURL, ws)

	entered := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- s.WithClaims(context.Background(), ws, fence, func(workspace.ClaimTx) error {
			close(entered)

			return nil
		})
	}()

	waitForLockWaiter(t, tx, done)

	select {
	case <-entered:
		t.Fatal("WithClaims ran while another transaction held the workspace row lock")
	case err := <-done:
		t.Fatalf("WithClaims returned %v while another transaction held the workspace row lock", err)
	default:
	}

	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("release the lock: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("WithClaims after the lock went = %v, want nil", err)
	}
}

// waitForLockWaiter asks the database, through tx, until a backend waits for
// a row lock in the statement that locks a workspace for its claims. Each
// round trip is the pause between two asks, so the test reads no clock; the
// test timeout ends a wait that never comes.
func waitForLockWaiter(t *testing.T, tx pgx.Tx, done <-chan error) {
	t.Helper()

	const ask = `SELECT count(*) FROM pg_stat_activity
WHERE wait_event_type = 'Lock' AND query LIKE '%FROM workspaces WHERE id%FOR UPDATE%'`

	for {
		select {
		case err := <-done:
			t.Fatalf("WithClaims returned %v while another transaction held the workspace row lock", err)
		default:
		}

		// The activity view is cached for the life of a transaction: drop the
		// cache, or the first answer stays.
		if _, err := tx.Exec(t.Context(), `SELECT pg_stat_clear_snapshot()`); err != nil {
			t.Fatalf("clear the activity snapshot: %v", err)
		}

		var waiting int

		if err := tx.QueryRow(t.Context(), ask).Scan(&waiting); err != nil {
			t.Fatalf("ask for the lock waiters: %v", err)
		}

		if waiting > 0 {
			return
		}
	}
}

// holdWorkspaceRowLock opens a second connection to the schema, takes the row
// lock of the workspace ws in a transaction and returns the transaction. The
// caller ends it.
func holdWorkspaceRowLock(t *testing.T, schemaURL string, ws workspace.ID) pgx.Tx {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Errorf("close the connection: %v", closeErr)
		}
	})

	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err = tx.Exec(t.Context(), `SELECT id FROM workspaces WHERE id = $1 FOR UPDATE`, ws.String()); err != nil {
		t.Fatalf("lock the workspace row: %v", err)
	}

	return tx
}

// TestRenewStatementHoldsItsRules sends hostile parameters straight to the
// renew statement, below every check of the store, the registry and the
// handler: another principal, another workspace, an unknown claim and a lease
// that ended each match no row and change nothing.
func TestRenewStatementHoldsItsRules(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	fence, ws := workspacetest.SeedClaimWorkspace(t, s)

	registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: s, Versions: s, Deleted: s, Manual: nil, Store: s, Now: nil, Lease: 0,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	claim, err := registry.Register(t.Context(), &workspace.RegisterClaimRequest{
		ViaJobToken: false, WorkspaceID: ws, Base: "sv-acme", Addresses: []string{"aws_instance.a"}, Replace: false,
		Principal: "ada", JobID: "", Trigger: "", Audit: workspacetest.AcceptEntry(fence),
	}, fence)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Errorf("close the connection: %v", closeErr)
		}
	})

	q := db.New(conn)

	ok := db.RenewWorkspaceClaimParams{
		LeaseEndsAtMs: claim.LeaseEndsAt.Add(time.Hour).UnixMilli(), NowMs: claim.StartedAt.UnixMilli(), ID: claim.ID,
		WorkspaceID: ws.String(), Principal: "ada",
	}

	wantRenewalsRefused(t, q, &claim, ok)

	got, err := registry.List(t.Context(), ws)
	if err != nil || len(got) != 1 || !got[0].LeaseEndsAt.Equal(claim.LeaseEndsAt) || !got[0].RenewedAt.IsZero() {
		t.Fatalf("claims after the hostile renewals = %+v, %v, want the claim as registered", got, err)
	}

	if _, renewErr := q.RenewWorkspaceClaim(t.Context(), ok); renewErr != nil {
		t.Fatalf("the statement with the rules met = %v, want a row", renewErr)
	}
}

// TestClaimTableRefusesALeaseThatDisagreesWithItsJob sends rows that break the
// CHECK of the table straight to the database: a claim of a job with a lease,
// and a claim with no job and no lease. A good row of each kind goes in.
func TestClaimTableRefusesALeaseThatDisagreesWithItsJob(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	_, ws := workspacetest.SeedClaimWorkspace(t, s)

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Errorf("close the connection: %v", closeErr)
		}
	})

	const insert = `INSERT INTO workspace_claims
(id, workspace_id, seq, base, addresses, principal, job_id, trigger_name, state, started_at_ms, ended_at_ms,
 lease_ends_at_ms, renewed_at_ms)
VALUES ($1, $2, $3, 'sv-acme', '["a"]', 'ada', $4, '', 'accepted', 1, 0, $5, 0)`

	for i, c := range []struct {
		name  string
		job   string
		lease int64
		ok    bool
	}{
		{"claim of a job with a lease", "job-1", 90, false},
		{"claim with no job and no lease", "", 0, false},
		{"claim of a job", "job-1", 0, true},
		{"claim with no job and a lease", "", 90, true},
	} {
		_, execErr := conn.Exec(t.Context(), insert, "clm-"+c.name, ws.String(), i+1, c.job, c.lease)
		if (execErr == nil) != c.ok {
			t.Errorf("%s: insert = %v, want ok %v", c.name, execErr, c.ok)
		}
	}
}

// TestClaimsOnTwoWorkspacesOfOneNamespaceInParallel proves that parallel
// registrations on two workspaces of one namespace all succeed and leave the
// audit chain of the namespace valid: the namespace row lock orders the audit
// appends of both workspaces.
func TestClaimsOnTwoWorkspacesOfOneNamespaceInParallel(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	s := open(t, base)
	fence, first := workspacetest.SeedClaimWorkspace(t, s)
	second := workspace.ID(first.String() + "-2")

	registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: s, Versions: s, Deleted: s, Manual: nil, Store: s, Now: nil, Lease: 0,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	const perWorkspace = 6

	targets := []struct {
		ws   workspace.ID
		base workspace.StateVersionID
	}{{first, "sv-acme"}, {second, "sv-acme-2"}}

	errs := make([]error, perWorkspace*len(targets))
	start := make(chan struct{})

	var wg sync.WaitGroup

	for i := range errs {
		target := targets[i%len(targets)]

		wg.Go(func() {
			<-start

			_, errs[i] = registry.Register(t.Context(), &workspace.RegisterClaimRequest{
				ViaJobToken: false, WorkspaceID: target.ws, Base: target.base,
				Addresses: []string{"aws_instance.n" + strconv.Itoa(i)}, Replace: false, Principal: "ada", JobID: "",
				Trigger: "", Audit: workspacetest.AcceptEntry(fence),
			}, fence)
		})
	}

	close(start)
	wg.Wait()

	for i, regErr := range errs {
		if regErr != nil {
			t.Errorf("registration %d: %v", i, regErr)
		}
	}

	for _, target := range targets {
		got, listErr := registry.List(t.Context(), target.ws)
		if listErr != nil || len(got) != perWorkspace {
			t.Errorf("claims of %s = %d, %v, want %d", target.ws, len(got), listErr, perWorkspace)
		}
	}

	invalid, err := audit.CheckScopes(t.Context(), s, []namespace.Name{fence.Namespace})
	if err != nil || invalid != 0 {
		t.Fatalf("audit chain of the namespace: %d breaks, %v, want none", invalid, err)
	}
}

// wantRenewalsRefused sends hostile parameters to the renew statement, one
// change of ok each, and fails the test for each that does not match no row.
func wantRenewalsRefused(t *testing.T, q *db.Queries, claim *workspace.Claim, ok db.RenewWorkspaceClaimParams) {
	t.Helper()

	for name, mutate := range map[string]func(p *db.RenewWorkspaceClaimParams){
		"another principal": func(p *db.RenewWorkspaceClaimParams) { p.Principal = "bob" },
		"another workspace": func(p *db.RenewWorkspaceClaimParams) { p.WorkspaceID += "-2" },
		"unknown claim":     func(p *db.RenewWorkspaceClaimParams) { p.ID = "clm-unknown" },
		"lease that ended":  func(p *db.RenewWorkspaceClaimParams) { p.NowMs = claim.LeaseEndsAt.UnixMilli() },
	} {
		p := ok
		mutate(&p)

		if _, renewErr := q.RenewWorkspaceClaim(t.Context(), p); !errors.Is(renewErr, pgx.ErrNoRows) &&
			!errors.Is(renewErr, sql.ErrNoRows) {
			t.Errorf("%s: statement = %v, want no rows", name, renewErr)
		}
	}
}
