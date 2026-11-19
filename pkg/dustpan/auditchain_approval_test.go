package dustpan_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// chainApprovalStore is the store surface the approval tests use: the audit
// chain state, the server scope list and check, and the atomic approval.
type chainApprovalStore interface {
	ChainRecord(ctx context.Context) (audit.ChainRecord, error)
	SaveChainRecord(ctx context.Context, rec audit.ChainRecord) error
	List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
	Check(ctx context.Context, ns namespace.Name) (audit.CheckResult, error)
	ApproveChainChange(
		ctx context.Context, e audit.Entry, fixed durable.StartRequest, retryKey string,
	) (durable.Operation, error)
}

// approvalOps lists the migration operations of ops.
func approvalOps(t *testing.T, ops durable.Store) []durable.Operation {
	t.Helper()

	found, err := ops.List(t.Context(), durable.Filter{Kind: auditChainKind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list migration operations: %v", err)
	}

	return found
}

// approvalEntryCount answers the count of approval entries in the server
// scope: entries with the action that records an approved chain change.
func approvalEntryCount(t *testing.T, st chainApprovalStore) int {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list server scope: %v", err)
	}

	var count int

	for i := range listing.Entries {
		if listing.Entries[i].Action == audit.ActionSetChain {
			count++
		}
	}

	return count
}

// saveWaitingChange stores a waiting change to off in st.
func saveWaitingChange(t *testing.T, st chainApprovalStore) {
	t.Helper()

	err := st.SaveChainRecord(t.Context(), audit.ChainRecord{
		ChangedAt: time.Now(), ChangedBy: "", State: audit.ChainStateOn, Pending: true,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	})
	if err != nil {
		t.Fatalf("save chain record: %v", err)
	}
}

// approvalCaller answers the caller entry of the store-level approvals.
func approvalCaller() audit.Entry {
	return audit.Entry{
		At: time.Now(), Namespace: audit.ServerScope, Subject: "alice", IDPSubject: "", TokenID: "tok-1",
		Action: audit.ActionSetChain, Object: audit.ServerScope.String(), ObjectID: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "req-1", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
		Origin:        nil,
	}
}

// approvalFixedRequest answers the start request of change under the fixed
// key of the migration.
func approvalFixedRequest(t *testing.T, change audit.ChainChange) durable.StartRequest {
	t.Helper()

	input, err := json.Marshal(change)
	if err != nil {
		t.Fatalf("encode change: %v", err)
	}

	return durable.StartRequest{Key: "audit-chain-migration", Kind: auditChainKind, Version: 1, Input: input}
}

// TestConcurrentApprovalsLeaveOneEntryAndOneOperation proves that concurrent
// approvals of one waiting change leave exactly one approval entry and one
// migration operation: only the approval whose start wins writes.
func TestConcurrentApprovalsLeaveOneEntryAndOneOperation(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		checkConcurrentApprovalsLeaveOneSQLite(t)
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		checkConcurrentApprovalsLeaveOnePostgres(t)
	})
}

// checkConcurrentApprovalsLeaveOneSQLite runs the concurrent case on SQLite.
func checkConcurrentApprovalsLeaveOneSQLite(t *testing.T) {
	t.Helper()

	st := openChainStore(t)
	ns := createChainNamespace(t, st)
	appendChainEntry(t, st, ns, "req-1")

	srv := runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	const approvals = 4

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		won     int
		refused int
	)

	for range approvals {
		wg.Go(func() {
			status, _ := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain")

			mu.Lock()
			defer mu.Unlock()

			switch status {
			case http.StatusAccepted:
				won++
			case http.StatusConflict:
				refused++
			default:
				t.Errorf("approve: %d, want 202 or 409", status)
			}
		})
	}

	wg.Wait()

	if won != 1 || refused != approvals-1 {
		t.Fatalf("concurrent approvals: won %d, refused %d, want 1 and %d", won, refused, approvals-1)
	}

	if got := approvalEntryCount(t, st); got != 1 {
		t.Fatalf("approval entries after concurrent approvals = %d, want 1", got)
	}

	if got := approvalOps(t, st.DurableOperations()); len(got) != 1 {
		t.Fatalf("migration operations after concurrent approvals = %d, want 1", len(got))
	}

	waitForRecord(t, st, func(rec audit.ChainRecord) bool {
		return rec.State == audit.ChainStateOff && !rec.Pending
	})
	waitForMaintenance(t, st, maintenance.StateNormal)
	srv.stop()
}

// checkConcurrentApprovalsLeaveOnePostgres runs the concurrent case on
// PostgreSQL, skipping without a URL.
func checkConcurrentApprovalsLeaveOnePostgres(t *testing.T) {
	t.Helper()

	base := postgresURL(t)
	st := openPostgresChainStore(t, base)
	ns := createPostgresNamespace(t, st)
	appendPostgresEntry(t, st, ns, "req-1")

	srv := runPostgresChainServer(t, st)
	waitForPostgresRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	const approvals = 4

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		won     int
		refused int
	)

	for range approvals {
		wg.Go(func() {
			status, _ := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain")

			mu.Lock()
			defer mu.Unlock()

			switch status {
			case http.StatusAccepted:
				won++
			case http.StatusConflict:
				refused++
			default:
				t.Errorf("approve: %d, want 202 or 409", status)
			}
		})
	}

	wg.Wait()

	if won != 1 || refused != approvals-1 {
		t.Fatalf("concurrent approvals: won %d, refused %d, want 1 and %d", won, refused, approvals-1)
	}

	if got := approvalEntryCount(t, st); got != 1 {
		t.Fatalf("approval entries after concurrent approvals = %d, want 1", got)
	}

	if got := migrationOpsPostgres(t, st); len(got) != 1 {
		t.Fatalf("migration operations after concurrent approvals = %d, want 1", len(got))
	}

	waitForPostgresRecord(t, st, func(rec audit.ChainRecord) bool {
		return rec.State == audit.ChainStateOff && !rec.Pending
	})
	waitForPostgresMaintenance(t, st, maintenance.StateNormal)
	srv.stop()
}

// TestLateApprovalAfterMigrationWritesNoEntry proves that an approval after
// the migration turned the chain off writes no entry: it answers conflict,
// and the chain check of every scope passes.
func TestLateApprovalAfterMigrationWritesNoEntry(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		checkLateApprovalWritesNoEntrySQLite(t)
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		checkLateApprovalWritesNoEntryPostgres(t)
	})
}

// checkLateApprovalWritesNoEntrySQLite runs the late case on SQLite.
func checkLateApprovalWritesNoEntrySQLite(t *testing.T) {
	t.Helper()

	st := openChainStore(t)
	ns := createChainNamespace(t, st)
	appendChainEntry(t, st, ns, "req-1")

	srv := runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })
	approveAndWait(t, st, srv, audit.ChainStateOff)

	if got := approvalEntryCount(t, st); got != 1 {
		t.Fatalf("approval entries after the migration = %d, want 1", got)
	}

	srv = runChainServer(t, st, audit.ChainOff)

	if status, _ := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain"); status != http.StatusConflict {
		t.Fatalf("late approval: %d, want 409", status)
	}

	if got := approvalEntryCount(t, st); got != 1 {
		t.Fatalf("approval entries after a late approval = %d, want 1", got)
	}

	for _, scope := range []namespace.Name{ns.Name, audit.ServerScope} {
		got, err := st.Check(t.Context(), scope)
		if err != nil || !got.Valid {
			t.Fatalf("check %s = %+v, %v; want valid", scope, got, err)
		}
	}

	srv.stop()
}

// checkLateApprovalWritesNoEntryPostgres runs the late case on PostgreSQL,
// skipping without a URL.
func checkLateApprovalWritesNoEntryPostgres(t *testing.T) {
	t.Helper()

	base := postgresURL(t)
	st := openPostgresChainStore(t, base)
	ns := createPostgresNamespace(t, st)
	appendPostgresEntry(t, st, ns, "req-1")

	srv := runPostgresChainServer(t, st)
	waitForPostgresRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	if status, body := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain"); status != http.StatusAccepted {
		t.Fatalf("approve: %d %s, want 202", status, body)
	}

	waitForPostgresRecord(t, st, func(rec audit.ChainRecord) bool {
		return rec.State == audit.ChainStateOff && !rec.Pending
	})
	waitForPostgresMaintenance(t, st, maintenance.StateNormal)
	srv.stop()

	if got := approvalEntryCount(t, st); got != 1 {
		t.Fatalf("approval entries after the migration = %d, want 1", got)
	}

	srv = runPostgresChainServer(t, st)

	if status, _ := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain"); status != http.StatusConflict {
		t.Fatalf("late approval: %d, want 409", status)
	}

	if got := approvalEntryCount(t, st); got != 1 {
		t.Fatalf("approval entries after a late approval = %d, want 1", got)
	}

	for _, scope := range []namespace.Name{ns.Name, audit.ServerScope} {
		got, err := st.Check(t.Context(), scope)
		if err != nil || !got.Valid {
			t.Fatalf("check %s = %+v, %v; want valid", scope, got, err)
		}
	}

	srv.stop()
}

// TestConflictingStartKeyLeavesNoEntry proves that a failed start leaves no
// entry: an approval whose fixed key names an operation of another kind
// answers key conflict and appends nothing.
func TestConflictingStartKeyLeavesNoEntry(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		checkConflictingStartKeyLeavesNoEntrySQLite(t)
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		checkConflictingStartKeyLeavesNoEntryPostgres(t)
	})
}

// checkConflictingStartKeyLeavesNoEntrySQLite runs the conflict case on
// SQLite.
func checkConflictingStartKeyLeavesNoEntrySQLite(t *testing.T) {
	t.Helper()

	st := openChainStore(t)
	saveWaitingChange(t, st)

	_, _, startErr := st.DurableOperations().Start(t.Context(), durable.StartRequest{
		Key: "audit-chain-migration", Kind: "other-kind", Version: 1, Input: nil,
	})
	if startErr != nil {
		t.Fatalf("start conflicting operation: %v", startErr)
	}

	change := audit.ChainChange{At: time.Now(), By: "alice", To: audit.ChainStateOff}
	caller := approvalCaller()

	_, approveErr := st.ApproveChainChange(
		t.Context(),
		audit.ChainSetEntry(caller, change),
		approvalFixedRequest(t, change),
		"audit-chain-migration-retry",
	)
	if !errors.Is(approveErr, durable.ErrKeyConflict) {
		t.Fatalf("approve with a conflicting start key: %v, want ErrKeyConflict", approveErr)
	}

	if got := approvalEntryCount(t, st); got != 0 {
		t.Fatalf("approval entries after a failed start = %d, want none", got)
	}
}

// checkConflictingStartKeyLeavesNoEntryPostgres runs the conflict case on
// PostgreSQL, skipping without a URL.
func checkConflictingStartKeyLeavesNoEntryPostgres(t *testing.T) {
	t.Helper()

	base := postgresURL(t)
	st := openPostgresChainStore(t, base)
	saveWaitingChange(t, st)

	_, _, startErr := st.DurableOperationsPort().Start(t.Context(), durable.StartRequest{
		Key: "audit-chain-migration", Kind: "other-kind", Version: 1, Input: nil,
	})
	if startErr != nil {
		t.Fatalf("start conflicting operation: %v", startErr)
	}

	change := audit.ChainChange{At: time.Now(), By: "alice", To: audit.ChainStateOff}
	caller := approvalCaller()

	_, approveErr := st.ApproveChainChange(
		t.Context(),
		audit.ChainSetEntry(caller, change),
		approvalFixedRequest(t, change),
		"audit-chain-migration-retry",
	)
	if !errors.Is(approveErr, durable.ErrKeyConflict) {
		t.Fatalf("approve with a conflicting start key: %v, want ErrKeyConflict", approveErr)
	}

	if got := approvalEntryCount(t, st); got != 0 {
		t.Fatalf("approval entries after a failed start = %d, want none", got)
	}
}

// errApprovalCommit is the injected failure of an approval commit.
var errApprovalCommit = errors.New("injected commit failure")

// failingApprovalStore wraps the store of a server and fails each atomic
// approval with err. Before it fails, it runs inside, then clears the waiting
// change, as the end of a migration of another approval does between the
// check and the commit of this one. keepPending leaves the change waiting.
type failingApprovalStore struct {
	*sqlite.Store

	err         error
	keepPending bool
	inside      func(ctx context.Context)
}

func (f failingApprovalStore) ApproveChainChange(
	ctx context.Context, e audit.Entry, fixed durable.StartRequest, retryKey string,
) (durable.Operation, error) {
	if f.err == nil {
		return f.Store.ApproveChainChange(ctx, e, fixed, retryKey)
	}

	if f.inside != nil {
		f.inside(ctx)
	}

	if f.keepPending {
		return durable.Operation{}, f.err
	}

	// The end of the migration of another approval does not depend on the
	// request of this one, so the clear runs with a context that a canceled
	// request does not end.
	other := context.WithoutCancel(ctx)

	rec, err := f.ChainRecord(other)
	if err != nil {
		return durable.Operation{}, err
	}

	rec.Pending = false

	err = f.SaveChainRecord(other, rec)
	if err != nil {
		return durable.Operation{}, err
	}

	return durable.Operation{}, f.err
}

// failingApprovalSetup is the state of the installation before an approval
// that fails to commit.
type failingApprovalSetup struct {
	// backToNormal puts the installation back to normal operation, with no
	// mark in the record, as the end of a migration does.
	backToNormal bool
	// clearMark clears the mark in the record only, so that the installation
	// stays in maintenance mode, as after an operator entered it.
	clearMark bool
}

// startFailingApproval starts a server whose approval commit fails, waits
// until its waiting change put the installation in maintenance mode, and
// applies setup.
func startFailingApproval(
	t *testing.T, store func(*sqlite.Store) failingApprovalStore, setup failingApprovalSetup,
) (*sqlite.Store, chainServer) {
	t.Helper()

	st := openChainStore(t)
	ns := createChainNamespace(t, st)
	appendChainEntry(t, st, ns, "req-1")

	srv := runChainServer(t, st, audit.ChainOff, dustpan.WithNamespaceStore(store(st)))
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })
	waitForMaintenance(t, st, maintenance.StateMaintenance)

	if setup.backToNormal || setup.clearMark {
		rec, err := st.ChainRecord(t.Context())
		if err != nil {
			t.Fatalf("read chain record: %v", err)
		}

		rec.EnteredMaintenance = false

		err = st.SaveChainRecord(t.Context(), rec)
		if err != nil {
			t.Fatalf("clear the maintenance mark: %v", err)
		}
	}

	if setup.backToNormal {
		err := st.SetState(t.Context(), maintenance.StateNormal)
		if err != nil {
			t.Fatalf("set normal operation: %v", err)
		}
	}

	return st, srv
}

// approvalOutcome is the state that a failed approval must leave.
type approvalOutcome struct {
	state maintenance.State
	// marked is whether the record notes that the chain entered maintenance mode.
	marked bool
}

// wantOutcome fails the test unless the installation and the record are in
// the state of want.
func wantOutcome(t *testing.T, st *sqlite.Store, want approvalOutcome) {
	t.Helper()

	if got, err := st.GetState(t.Context()); err != nil || got != want.state {
		t.Fatalf("state after a failed approval = %q, %v, want %q", got, err, want.state)
	}

	rec, err := st.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("read chain record: %v", err)
	}

	if rec.EnteredMaintenance != want.marked {
		t.Fatalf("record after a failed approval = %+v, want maintenance mark %v", rec, want.marked)
	}
}

// TestApprovalThatEntersMaintenanceAndFailsLeavesIt proves that an approval
// that moved the installation from normal operation to maintenance mode and
// then fails to commit puts the installation back to normal operation and
// clears the mark in the stored record. The test puts the installation back
// to normal operation by hand, as the end of a migration of another approval
// does, before it sends the approval.
func TestApprovalThatEntersMaintenanceAndFailsLeavesIt(t *testing.T) {
	t.Parallel()

	st, srv := startFailingApproval(t, func(s *sqlite.Store) failingApprovalStore {
		return failingApprovalStore{Store: s, err: errApprovalCommit, keepPending: false, inside: nil}
	}, failingApprovalSetup{backToNormal: true, clearMark: false})

	if status, body := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain"); status == http.StatusAccepted {
		t.Fatalf("approve with a failing commit: %d %s, want a failure", status, body)
	}

	wantOutcome(t, st, approvalOutcome{state: maintenance.StateNormal, marked: false})

	srv.stop()
}

// TestFailedApprovalKeepsMaintenanceThatAWaitingChainEntered proves that an
// approval that did not move the installation from normal operation does not
// leave maintenance mode when it fails: the waiting change entered it, and
// the mark in the record stays.
func TestFailedApprovalKeepsMaintenanceThatAWaitingChainEntered(t *testing.T) {
	t.Parallel()

	st, srv := startFailingApproval(t, func(s *sqlite.Store) failingApprovalStore {
		return failingApprovalStore{Store: s, err: errApprovalCommit, keepPending: false, inside: nil}
	}, failingApprovalSetup{backToNormal: false, clearMark: false})

	if status, body := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain"); status == http.StatusAccepted {
		t.Fatalf("approve with a failing commit: %d %s, want a failure", status, body)
	}

	wantOutcome(t, st, approvalOutcome{state: maintenance.StateMaintenance, marked: true})

	srv.stop()
}

// TestFailedApprovalKeepsMaintenanceThatAnOperatorEntered proves that the
// approval reads the state before it enters: an installation that already
// is in maintenance mode is not marked as entered by the approval, and a
// failed approval leaves it in maintenance mode with no mark.
func TestFailedApprovalKeepsMaintenanceThatAnOperatorEntered(t *testing.T) {
	t.Parallel()

	st, srv := startFailingApproval(t, func(s *sqlite.Store) failingApprovalStore {
		return failingApprovalStore{Store: s, err: errApprovalCommit, keepPending: false, inside: nil}
	}, failingApprovalSetup{backToNormal: false, clearMark: true})

	if status, body := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain"); status == http.StatusAccepted {
		t.Fatalf("approve with a failing commit: %d %s, want a failure", status, body)
	}

	wantOutcome(t, st, approvalOutcome{state: maintenance.StateMaintenance, marked: false})

	srv.stop()
}

// TestFailedApprovalWithAWaitingChangeKeepsMaintenanceAndReportsOnlyTheCommit
// proves that the undo treats the refusal of a waiting change as expected:
// the change still waits, so maintenance mode stays, the mark stays, and the
// answer is the commit failure only, with no failure of the undo.
func TestFailedApprovalWithAWaitingChangeKeepsMaintenanceAndReportsOnlyTheCommit(t *testing.T) {
	t.Parallel()

	st, srv := startFailingApproval(t, func(s *sqlite.Store) failingApprovalStore {
		return failingApprovalStore{Store: s, err: errApprovalCommit, keepPending: true, inside: nil}
	}, failingApprovalSetup{backToNormal: true, clearMark: false})

	status, body := srv.do(t, http.MethodPost, "/api/dustpan/v1/audit/chain")
	if status == http.StatusAccepted {
		t.Fatalf("approve with a failing commit: %d %s, want a failure", status, body)
	}

	if strings.Contains(body, "maintenance.held") || status == http.StatusConflict {
		t.Fatalf("answer = %d %q, want the commit failure, not the refusal of the undo", status, body)
	}

	wantOutcome(t, st, approvalOutcome{state: maintenance.StateMaintenance, marked: true})

	srv.stop()
}

// TestApprovalOfACanceledRequestStillUndoesItsEntry proves that the undo of
// a failed approval does not depend on the request context: the client
// cancels its request while the commit runs, and the installation still goes
// back to normal operation.
func TestApprovalOfACanceledRequestStillUndoesItsEntry(t *testing.T) {
	t.Parallel()

	inside := make(chan struct{})

	st, srv := startFailingApproval(t, func(s *sqlite.Store) failingApprovalStore {
		return failingApprovalStore{
			Store: s, err: errApprovalCommit, keepPending: false,
			inside: func(ctx context.Context) {
				close(inside)
				<-ctx.Done()
			},
		}
	}, failingApprovalSetup{backToNormal: true, clearMark: false})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.base+"/api/dustpan/v1/audit/chain", http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	authorize(t, req, adminToken)

	sent := make(chan error, 1)

	go func() {
		resp, doErr := http.DefaultClient.Do(req)
		if doErr == nil {
			doErr = resp.Body.Close()
		}

		sent <- doErr
	}()

	<-inside
	cancel()

	if doErr := <-sent; doErr == nil {
		t.Fatal("a canceled request got an answer, want a client error")
	}

	// The undo leaves maintenance mode first and clears the mark after it.
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return !rec.EnteredMaintenance })
	wantOutcome(t, st, approvalOutcome{state: maintenance.StateNormal, marked: false})

	srv.stop()
}

// TestApprovalAfterACanceledMigrationStartsOneOperation proves the retry of
// a failed migration: after the fixed operation ends canceled with the
// change still waiting, an approval starts one operation under a new key
// and writes exactly one entry for it.
func TestApprovalAfterACanceledMigrationStartsOneOperation(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		checkApprovalAfterCanceledMigrationSQLite(t)
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()
		checkApprovalAfterCanceledMigrationPostgres(t)
	})
}

// checkApprovalAfterCanceledMigrationSQLite runs the retry case on SQLite.
func checkApprovalAfterCanceledMigrationSQLite(t *testing.T) {
	t.Helper()

	st := openChainStore(t)
	saveWaitingChange(t, st)

	change := audit.ChainChange{At: time.Now(), By: "alice", To: audit.ChainStateOff}
	caller := approvalCaller()

	first, err := st.ApproveChainChange(
		t.Context(),
		audit.ChainSetEntry(caller, change),
		approvalFixedRequest(t, change),
		"audit-chain-migration-retry",
	)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	if cancelErr := st.DurableOperations().Cancel(t.Context(), first.ID); cancelErr != nil {
		t.Fatalf("cancel migration operation: %v", cancelErr)
	}

	second, err := st.ApproveChainChange(
		t.Context(),
		audit.ChainSetEntry(caller, change),
		approvalFixedRequest(t, change),
		"audit-chain-migration-retry",
	)
	if err != nil {
		t.Fatalf("approve after a canceled migration: %v", err)
	}

	if second.ID == first.ID {
		t.Fatalf("retry gave %s, want a new operation", second.ID)
	}

	if got := approvalOps(t, st.DurableOperations()); len(got) != 2 {
		t.Fatalf("migration operations after the retry = %d, want 2", len(got))
	}

	if got := approvalEntryCount(t, st); got != 2 {
		t.Fatalf("approval entries after the retry = %d, want one per approval", got)
	}
}

// checkApprovalAfterCanceledMigrationPostgres runs the retry case on
// PostgreSQL, skipping without a URL.
func checkApprovalAfterCanceledMigrationPostgres(t *testing.T) {
	t.Helper()

	base := postgresURL(t)
	st := openPostgresChainStore(t, base)
	saveWaitingChange(t, st)

	change := audit.ChainChange{At: time.Now(), By: "alice", To: audit.ChainStateOff}
	caller := approvalCaller()

	ops := st.DurableOperationsPort()

	first, err := st.ApproveChainChange(
		t.Context(),
		audit.ChainSetEntry(caller, change),
		approvalFixedRequest(t, change),
		"audit-chain-migration-retry",
	)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	if cancelErr := ops.Cancel(t.Context(), first.ID); cancelErr != nil {
		t.Fatalf("cancel migration operation: %v", cancelErr)
	}

	second, err := st.ApproveChainChange(
		t.Context(),
		audit.ChainSetEntry(caller, change),
		approvalFixedRequest(t, change),
		"audit-chain-migration-retry",
	)
	if err != nil {
		t.Fatalf("approve after a canceled migration: %v", err)
	}

	if second.ID == first.ID {
		t.Fatalf("retry gave %s, want a new operation", second.ID)
	}

	if got := migrationOpsPostgres(t, st); len(got) != 2 {
		t.Fatalf("migration operations after the retry = %d, want 2", len(got))
	}

	if got := approvalEntryCount(t, st); got != 2 {
		t.Fatalf("approval entries after the retry = %d, want one per approval", got)
	}
}

// postgresURL answers the PostgreSQL URL of the environment, skipping the
// test without it.
func postgresURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv(postgresURLEnv)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL case", postgresURLEnv)
	}

	return base
}
