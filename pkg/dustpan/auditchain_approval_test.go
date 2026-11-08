package dustpan_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
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

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
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
