package dustpan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// errNoStep is the refusal of stepFreshness.
var errNoStep = errors.New("no MFA step")

// errRestoreCheck is the failure of the restore check in the tests.
var errRestoreCheck = errors.New("restore check fails")

// stepFreshness asks for an MFA step that the caller did not pass.
type stepFreshness struct{}

func (stepFreshness) RequireFresh(context.Context, string) error { return errNoStep }

// newTestAuditChain answers the chain of a fresh SQLite store with setting,
// where a change to the other state waits.
func newTestAuditChain(t *testing.T, setting audit.ChainSetting) *auditChain {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.auditChain = setting

	chain, err := newAuditChain(cfg, nil)
	if err != nil || chain == nil {
		t.Fatalf("new audit chain = %v, %v", chain, err)
	}

	chain.migrationsRunning.Store(true)

	err = st.SaveChainRecord(t.Context(), audit.ChainRecord{
		ChangedAt: time.Now(), ChangedBy: "", State: audit.ChainStateOn, Pending: true,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	})
	if err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	return chain
}

// approver is the caller of the approvals of the tests.
func approver() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "alice", IDPSubject: "", TokenID: "tok-1",
		Action: audit.ActionSetChain, Object: "", ObjectID: "", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test",
		Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "req-1",
		PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// createServicePrincipal stores a service principal with subject in the
// store behind chain.
func createServicePrincipal(t *testing.T, chain *auditChain, subject string) {
	t.Helper()

	st, ok := chain.namespaces.(principal.RecordStore)
	if !ok {
		t.Fatal("namespace store keeps no principals")
	}

	at := time.Now()

	p := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		DisabledAt:  time.Time{},
		ID:          "prn-" + subject,
		Subject:     subject,
		DisplayName: subject,
		Kind:        principal.KindService,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	e := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "server:manage-principals", Object: "principal:prn-" + subject, ObjectID: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}

	if err := st.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create service principal %q: %v", subject, err)
	}
}

// TestApproveOffRefusesServicePrincipal proves that an approval of a change
// to off by a service principal is refused before it writes anything, that
// the same caller may approve a change to on, and that a subject with no
// principal row may approve a change to off.
func TestApproveOffRefusesServicePrincipal(t *testing.T) {
	t.Parallel()

	off := newTestAuditChain(t, audit.ChainOff)
	createServicePrincipal(t, off, "svc-ci")

	caller := approver()
	caller.Subject = "svc-ci"

	if err := off.Approve(t.Context(), caller); !errors.Is(err, audit.ErrChainNeedsPerson) {
		t.Fatalf("approve off as a service principal: %v, want ErrChainNeedsPerson", err)
	}

	if got := migrationOpCount(t, off); got != 0 {
		t.Fatalf("operations after a refused approval = %d, want none", got)
	}

	if err := off.Approve(t.Context(), approver()); err != nil {
		t.Fatalf("approve off without a principal row: %v", err)
	}

	on := newTestAuditChain(t, audit.ChainOn)
	createServicePrincipal(t, on, "svc-ci")

	if err := on.Approve(t.Context(), caller); err != nil {
		t.Fatalf("approve on as a service principal: %v", err)
	}
}

// TestApproveOffNeedsTheMFAStep proves that once the MFA check asks for a
// step, an approval of a change to off without it is refused before it
// writes anything, and that a change to on asks for no step.
func TestApproveOffNeedsTheMFAStep(t *testing.T) {
	t.Parallel()

	off := newTestAuditChain(t, audit.ChainOff)
	off.freshness = stepFreshness{}

	if err := off.Approve(t.Context(), approver()); !errors.Is(err, audit.ErrChainStepRequired) ||
		!errors.Is(err, errNoStep) {
		t.Fatalf("approve off without the step: %v, want ErrChainStepRequired", err)
	}

	if got := migrationOpCount(t, off); got != 0 {
		t.Fatalf("operations after a refused approval = %d, want none", got)
	}

	on := newTestAuditChain(t, audit.ChainOn)
	on.freshness = stepFreshness{}

	if err := on.Approve(t.Context(), approver()); err != nil {
		t.Fatalf("approve on: %v, want no MFA step", err)
	}

	if got := migrationOpCount(t, on); got != 1 {
		t.Fatalf("operations after the approval = %d, want 1", got)
	}

	if err := on.Approve(t.Context(), approver()); !errors.Is(err, audit.ErrChainMigrating) {
		t.Fatalf("second approval: %v, want ErrChainMigrating", err)
	}
}

// TestApproveOffWithoutMFAAsksForNoStep proves that without an MFA check,
// which built-in MFA does not have yet, a change to off asks for no step.
func TestApproveOffWithoutMFAAsksForNoStep(t *testing.T) {
	t.Parallel()

	off := newTestAuditChain(t, audit.ChainOff)

	if err := off.Approve(t.Context(), approver()); err != nil {
		t.Fatalf("approve off: %v", err)
	}

	got := migrationChange(t, off)
	if got.To != audit.ChainStateOff || got.By != "alice" {
		t.Fatalf("started change = %+v, want off by alice", got)
	}
}

// TestApprovalAndRestoreRefuseEachOther proves that a restore is refused
// while a change waits, and an approval is refused while a restore runs.
func TestApprovalAndRestoreRefuseEachOther(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOff)

	if err := chain.admitRestore(t.Context()); !errors.Is(err, audit.ErrChainChangeWaits) {
		t.Fatalf("restore while a change waits: %v, want ErrChainChangeWaits", err)
	}

	chain.restoreRunning = func(context.Context) (bool, error) { return true, nil }

	if err := chain.Approve(t.Context(), approver()); !errors.Is(err, backup.ErrRestoreRunning) {
		t.Fatalf("approve while a restore runs: %v, want ErrRestoreRunning", err)
	}

	if got := migrationOpCount(t, chain); got != 0 {
		t.Fatalf("operations after a refused approval = %d, want none", got)
	}
}

// TestApproveWithNothingToApprove proves that an approval while no change
// waits is refused.
func TestApproveWithNothingToApprove(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOn)

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = false
	if err := chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	if err := chain.Approve(t.Context(), approver()); !errors.Is(err, audit.ErrNoChainChange) {
		t.Fatalf("approve with nothing waiting: %v, want ErrNoChainChange", err)
	}

	if err := chain.admitRestore(t.Context()); err != nil {
		t.Fatalf("restore with nothing waiting: %v, want it admitted", err)
	}
}

// errApprovalStart is the injected failure of a migration start.
var errApprovalStart = errors.New("injected approval start failure")

// failOnceApproval wraps the atomic approval of a chain and fails its first
// call with err, then delegates to the real approval.
type failOnceApproval struct {
	real chainApprovalStore
	err  error
	fail bool
}

func (f *failOnceApproval) ApproveChainChange(
	ctx context.Context, e audit.Entry, fixed durable.StartRequest, retryKey string,
) (durable.Operation, error) {
	if f.fail {
		f.fail = false

		return durable.Operation{}, f.err
	}

	return f.real.ApproveChainChange(ctx, e, fixed, retryKey)
}

// approvalEntriesOf answers the approval entries of chain in the server
// scope: entries with the action that records an approved chain change.
func approvalEntriesOf(t *testing.T, chain *auditChain) []audit.Entry {
	t.Helper()

	listing, err := chain.entries.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list server scope: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Action == audit.ActionSetChain {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

// TestFailedApprovalWritesNoEntryAndRetryWritesOne proves that a failed
// start leaves no entry: the first approval fails, writes no approval entry
// and starts no operation, and the retry writes exactly one of each.
func TestFailedApprovalWritesNoEntryAndRetryWritesOne(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOff)
	chain.approve = &failOnceApproval{real: chain.approve, err: errApprovalStart, fail: true}

	if err := chain.Approve(t.Context(), approver()); !errors.Is(err, errApprovalStart) {
		t.Fatalf("approve with a failed start: %v, want the injected failure", err)
	}

	if got := approvalEntriesOf(t, chain); len(got) != 0 {
		t.Fatalf("approval entries after a failed start = %d, want none", len(got))
	}

	if got := migrationOpCount(t, chain); got != 0 {
		t.Fatalf("operations after a failed start = %d, want none", got)
	}

	if err := chain.Approve(t.Context(), approver()); err != nil {
		t.Fatalf("retry after a failed start: %v", err)
	}

	if got := approvalEntriesOf(t, chain); len(got) != 1 {
		t.Fatalf("approval entries after the retry = %d, want 1", len(got))
	}

	if got := migrationOpCount(t, chain); got != 1 {
		t.Fatalf("operations after the retry = %d, want 1", got)
	}
}

// newConcurrentApprovalChain answers a chain on st with the migration loop
// marked running, ready for an approval of a change to off.
func newConcurrentApprovalChain(t *testing.T, st *sqlite.Store) *auditChain {
	t.Helper()

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.auditChain = audit.ChainOff

	chain, err := newAuditChain(cfg, nil)
	if err != nil || chain == nil {
		t.Fatalf("new audit chain = %v, %v", chain, err)
	}

	chain.migrationsRunning.Store(true)

	return chain
}

// saveConcurrentWaitingChange stores a waiting change to on in st.
func saveConcurrentWaitingChange(t *testing.T, st *sqlite.Store) {
	t.Helper()

	saveErr := st.SaveChainRecord(t.Context(), audit.ChainRecord{
		ChangedAt: time.Now(), ChangedBy: "", State: audit.ChainStateOn, Pending: true,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	})
	if saveErr != nil {
		t.Fatalf("save chain record: %v", saveErr)
	}
}

// approvalCounts is the outcome of runConcurrentApprovals.
type approvalCounts struct {
	won  int
	held int
}

// runConcurrentApprovals approves with every chain at the same time and
// answers the count of approvals that won and the count refused while a
// migration runs.
func runConcurrentApprovals(t *testing.T, chains []*auditChain) approvalCounts {
	t.Helper()

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		won, held int
	)

	for _, chain := range chains {
		wg.Go(func() {
			err := chain.Approve(t.Context(), approver())

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				won++
			case errors.Is(err, audit.ErrChainMigrating):
				held++
			default:
				t.Errorf("approve: %v, want nil or ErrChainMigrating", err)
			}
		})
	}

	wg.Wait()

	return approvalCounts{won: won, held: held}
}

// TestConcurrentApprovalsLeaveOneEntry proves that two approvals of one
// waiting change that run at the same time leave exactly one approval entry
// and one operation: the approval whose start loses writes nothing.
func TestConcurrentApprovalsLeaveOneEntry(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	first := newConcurrentApprovalChain(t, st)
	second := newConcurrentApprovalChain(t, st)

	saveConcurrentWaitingChange(t, st)

	counts := runConcurrentApprovals(t, []*auditChain{first, second})

	if counts.won != 1 || counts.held != 1 {
		t.Fatalf("concurrent approvals: won %d, held %d, want 1 and 1", counts.won, counts.held)
	}

	if got := approvalEntriesOf(t, first); len(got) != 1 {
		t.Fatalf("approval entries after concurrent approvals = %d, want 1", len(got))
	}

	if got := migrationOpCount(t, first); got != 1 {
		t.Fatalf("operations after concurrent approvals = %d, want 1", got)
	}
}

// TestConcurrentApprovalsOnOneStore proves that two instances on the same
// store approve at the same time, and exactly one wins: the other answers
// ErrChainMigrating.
func TestConcurrentApprovalsOnOneStore(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	newChain := func() *auditChain {
		t.Helper()

		cfg := defaultSettings()
		cfg.namespaceStore = st
		cfg.auditChain = audit.ChainOff

		chain, err := newAuditChain(cfg, nil)
		if err != nil || chain == nil {
			t.Fatalf("new audit chain = %v, %v", chain, err)
		}

		chain.migrationsRunning.Store(true)

		return chain
	}

	first, second := newChain(), newChain()

	saveErr := st.SaveChainRecord(t.Context(), audit.ChainRecord{
		ChangedAt: time.Now(), ChangedBy: "", State: audit.ChainStateOn, Pending: true,
		EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: false,
	})
	if saveErr != nil {
		t.Fatalf("save chain record: %v", saveErr)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		won  int
		held int
	)

	for _, chain := range []*auditChain{first, second} {
		wg.Go(func() {
			err := chain.Approve(t.Context(), approver())

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				won++
			case errors.Is(err, audit.ErrChainMigrating):
				held++
			default:
				t.Errorf("approve: %v, want nil or ErrChainMigrating", err)
			}
		})
	}

	wg.Wait()

	if won != 1 || held != 1 {
		t.Fatalf("concurrent approvals: won %d, held %d, want 1 and 1", won, held)
	}
}

// TestApproveWithoutMigrationLoop proves that an approval while the server
// runs no migration loop stops before it starts anything.
func TestApproveWithoutMigrationLoop(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOff)
	chain.migrationsRunning.Store(false)

	if err := chain.Approve(t.Context(), approver()); !errors.Is(err, audit.ErrChainMigrationsStopped) {
		t.Fatalf("approve without the migration loop: %v, want ErrChainMigrationsStopped", err)
	}

	if got := migrationOpCount(t, chain); got != 0 {
		t.Fatalf("operations after a refused approval = %d, want none", got)
	}

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	if !rec.Pending {
		t.Fatalf("chain record = %+v, want the waiting change", rec)
	}
}

// migrationOpCount answers the count of migration operations of chain.
func migrationOpCount(t *testing.T, chain *auditChain) int {
	t.Helper()

	ops, err := chain.durableOps.List(t.Context(), durable.Filter{
		Kind:   auditChainKind,
		States: nil,
		Limit:  0,
	})
	if err != nil {
		t.Fatalf("list migration operations: %v", err)
	}

	return len(ops)
}

// migrationChange answers the approved change of the single migration
// operation of chain.
func migrationChange(t *testing.T, chain *auditChain) audit.ChainChange {
	t.Helper()

	ops, err := chain.durableOps.List(t.Context(), durable.Filter{
		Kind:   auditChainKind,
		States: nil,
		Limit:  0,
	})
	if err != nil {
		t.Fatalf("list migration operations: %v", err)
	}

	if len(ops) != 1 {
		t.Fatalf("migration operations = %d, want 1", len(ops))
	}

	change, err := decodeMigrationChange(ops[0])
	if err != nil {
		t.Fatalf("decode migration change: %v", err)
	}

	return change
}

// newTestCheckpointSigner returns a signer whose seed repeats 7.
func newTestCheckpointSigner(t *testing.T) *audit.Signer {
	t.Helper()

	signer, err := audit.NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	return signer
}

// openTestBlobs opens a blob store in a temporary directory that the test
// closes.
func openTestBlobs(t *testing.T) *fs.Store {
	t.Helper()

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// pendingTestEntry returns a valid entry of the scope @server.
func pendingTestEntry() audit.Entry {
	return audit.Entry{
		At:            time.Now(),
		Namespace:     audit.ServerScope,
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(identity.ActionServerManageAudit),
		Object:        audit.ServerScope.String(),
		ObjectID:      "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-1",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// checkpointCount answers the count of checkpoint objects of blobs.
func checkpointCount(t *testing.T, blobs *fs.Store) int {
	t.Helper()

	objects, err := blobtest.ListAll(t.Context(), blobs, audit.CheckpointPrefix+"/")
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}

	return len(objects)
}

// metricLine answers the metrics output of the observed registry of chain.
func metricLine(t *testing.T, chain *auditChain) string {
	t.Helper()

	var out strings.Builder

	if err := chain.observed.Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	return out.String()
}

// TestNewEpochAfterRestoreWritesItsEntryInMaintenance proves that the
// after-apply step of a restore writes its entry in @server while the real
// stored installation state is maintenance: a restore holds maintenance
// mode until its last step ends, and a checked append would lose the entry.
func TestNewEpochAfterRestoreWritesItsEntryInMaintenance(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOn)

	reader, ok := chain.entries.(audit.EntryReader)
	if !ok {
		t.Fatal("audit store reads no single entry")
	}

	state, ok := chain.store.(maintenance.Store)
	if !ok {
		t.Fatal("chain store holds no installation state")
	}

	cps := audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs: openTestBlobs(t), Entries: reader, Signer: newTestCheckpointSigner(t), Marks: nil, Now: nil,
	})

	if err := state.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	step := newEpochAfterRestore(cps, chain.server, time.Now)
	if err := step(t.Context()); err != nil {
		t.Fatalf("after-apply step in maintenance mode: %v", err)
	}

	last, found, err := chain.store.LastEntry(t.Context(), audit.ServerScope)
	if err != nil || !found || last.ValuePath != "/audit/checkpoints/epoch/1" {
		t.Fatalf("last @server entry = %+v, %v, %v; want the epoch 1 entry", last, found, err)
	}
}

// TestCheckWritesNoCheckpointWhileAChangeWaits proves that with checkpoints
// on, check runs the chain check and the checkpoint check but writes no
// checkpoint while a change of the chain waits. After the wait ends, the
// next check signs the head.
func TestCheckWritesNoCheckpointWhileAChangeWaits(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOn)

	blobs := openTestBlobs(t)

	reader, ok := chain.entries.(audit.EntryReader)
	if !ok {
		t.Fatal("audit store reads no single entry")
	}

	chain.checkpoints = audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs:   blobs,
		Entries: reader,
		Signer:  newTestCheckpointSigner(t),
		Marks:   nil,
		Now:     nil,
	})
	chain.observed = metrics.NewServerMetrics("test", time.Now())

	stored, err := chain.server.AppendServer(t.Context(), pendingTestEntry())
	if err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	if stored.Hash == "" {
		t.Fatal("server entry: no hash, want the chain on")
	}

	chain.check(t.Context())

	if got := checkpointCount(t, blobs); got != 0 {
		t.Fatalf("checkpoints while a change waits: got %d objects, want none", got)
	}

	if got := metricLine(t, chain); !strings.Contains(got, "dustpan_audit_chain_invalid 0") {
		t.Fatalf("metrics after the check while a change waits:\n%s\nwant no invalid scope", got)
	}

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = false

	if err := chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	chain.check(t.Context())

	if got := checkpointCount(t, blobs); got != 1 {
		t.Fatalf("checkpoints after the wait: got %d objects, want one", got)
	}
}

// TestCheckWritesNoCheckpointWhileRestoreRuns proves that with checkpoints
// on, check runs the chain check and the checkpoint check but writes no
// checkpoint while a restore runs. After the restore ends, the next check
// signs the head.
func TestCheckWritesNoCheckpointWhileRestoreRuns(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOn)

	blobs := openTestBlobs(t)

	reader, ok := chain.entries.(audit.EntryReader)
	if !ok {
		t.Fatal("audit store reads no single entry")
	}

	chain.checkpoints = audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs:   blobs,
		Entries: reader,
		Signer:  newTestCheckpointSigner(t),
		Marks:   nil,
		Now:     nil,
	})
	chain.observed = metrics.NewServerMetrics("test", time.Now())

	stored, err := chain.server.AppendServer(t.Context(), pendingTestEntry())
	if err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	if stored.Hash == "" {
		t.Fatal("server entry: no hash, want the chain on")
	}

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = false

	if err := chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	chain.restoreRunning = func(context.Context) (bool, error) { return true, nil }

	chain.check(t.Context())

	if got := checkpointCount(t, blobs); got != 0 {
		t.Fatalf("checkpoints while a restore runs: got %d objects, want none", got)
	}

	if got := metricLine(t, chain); !strings.Contains(got, "dustpan_audit_chain_invalid 0") {
		t.Fatalf("metrics after the check while a restore runs:\n%s\nwant no invalid scope", got)
	}

	chain.restoreRunning = func(context.Context) (bool, error) { return false, nil }

	chain.check(t.Context())

	if got := checkpointCount(t, blobs); got != 1 {
		t.Fatalf("checkpoints after the restore: got %d objects, want one", got)
	}
}

// TestCheckWritesNoCheckpointWhenRestoreCheckFails proves that when the
// restore check fails, check writes no checkpoint.
func TestCheckWritesNoCheckpointWhenRestoreCheckFails(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOn)

	blobs := openTestBlobs(t)

	reader, ok := chain.entries.(audit.EntryReader)
	if !ok {
		t.Fatal("audit store reads no single entry")
	}

	chain.checkpoints = audit.NewCheckpoints(audit.CheckpointsConfig{
		Blobs:   blobs,
		Entries: reader,
		Signer:  newTestCheckpointSigner(t),
		Marks:   nil,
		Now:     nil,
	})
	chain.observed = metrics.NewServerMetrics("test", time.Now())

	stored, err := chain.server.AppendServer(t.Context(), pendingTestEntry())
	if err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	if stored.Hash == "" {
		t.Fatal("server entry: no hash, want the chain on")
	}

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = false

	if err := chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	chain.restoreRunning = func(context.Context) (bool, error) { return false, errRestoreCheck }

	chain.check(t.Context())

	if got := checkpointCount(t, blobs); got != 0 {
		t.Fatalf("checkpoints when the restore check fails: got %d objects, want none", got)
	}
}

// invalidScopes matches a dustpan_audit_chain_invalid series above zero.
var invalidScopes = regexp.MustCompile(`(?m)^dustpan_audit_chain_invalid [1-9]`)

// errInjected is the failure that the wrappers of the check tests inject.
var errInjected = errors.New("injected failure")

// failingCheck is an audit store whose Check fails.
type failingCheck struct {
	audit.Store
}

func (failingCheck) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: false}, errInjected
}

// failingPut is a blob store whose Put fails.
type failingPut struct {
	blob.Store
}

func (failingPut) Put(context.Context, blob.Key, io.Reader) error { return errInjected }

// TestCheckCountsAFailedCallAsNotChecked proves that a failed read of the
// chain or a failed checkpoint write counts in
// dustpan_audit_chain_check_errors_total and never in
// dustpan_audit_chain_invalid, and that such a check does not lower the
// gauge that an earlier check set.
func TestCheckCountsAFailedCallAsNotChecked(t *testing.T) {
	t.Parallel()

	tests := []failCase{
		{name: "chain read fails", checkpoints: false, failRead: true, failPut: false},
		{name: "chain read fails with checkpoints", checkpoints: true, failRead: true, failPut: false},
		{name: "checkpoint write fails", checkpoints: true, failRead: false, failPut: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			chain := newFailingTestChain(t, tt)

			chain.check(t.Context())

			got := metricLine(t, chain)
			if invalidScopes.MatchString(got) {
				t.Fatalf("metrics after a failed call:\n%s\nwant no invalid scope", got)
			}

			wantMetric(t, got, "dustpan_audit_chain_check_errors_total 1")

			chain.observed.SetAuditChainInvalid(2)
			chain.check(t.Context())

			got = metricLine(t, chain)
			wantMetric(t, got, "dustpan_audit_chain_invalid 2")
			wantMetric(t, got, "dustpan_audit_chain_check_errors_total 2")
		})
	}
}

// failCase names the calls that fail in a check of newFailingTestChain.
// With checkpoints set, the chain signs checkpoints. failRead makes the
// chain check fail, and failPut makes the checkpoint write fail.
type failCase struct {
	name        string
	checkpoints bool
	failRead    bool
	failPut     bool
}

// newFailingTestChain answers a chain with one entry in @server and no
// change that waits, whose calls fail as fc says.
func newFailingTestChain(t *testing.T, fc failCase) *auditChain {
	t.Helper()

	chain := newTestAuditChain(t, audit.ChainOn)

	reader, ok := chain.entries.(audit.EntryReader)
	if !ok {
		t.Fatal("audit store reads no single entry")
	}

	if fc.checkpoints {
		var blobs blob.Store = openTestBlobs(t)
		if fc.failPut {
			blobs = failingPut{Store: blobs}
		}

		chain.checkpoints = audit.NewCheckpoints(audit.CheckpointsConfig{
			Blobs:   blobs,
			Entries: reader,
			Signer:  newTestCheckpointSigner(t),
			Marks:   nil,
			Now:     nil,
		})
	}

	if fc.failRead {
		chain.entries = failingCheck{Store: chain.entries}
	}

	chain.observed = metrics.NewServerMetrics("test", time.Now())

	if _, err := chain.server.AppendServer(t.Context(), pendingTestEntry()); err != nil {
		t.Fatalf("append server entry: %v", err)
	}

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = false

	if err := chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}

	return chain
}

// wantMetric fails the test unless the metrics output got holds line.
func wantMetric(t *testing.T, got, line string) {
	t.Helper()

	if !strings.Contains(got, line) {
		t.Fatalf("metrics:\n%s\nwant %q", got, line)
	}
}

// TestRestoredLogCheckCountsAFailedCallAsNotChecked proves that a failed
// read in the check of a restored log counts in
// dustpan_audit_chain_check_errors_total, not as a restore whose log
// failed the check.
func TestRestoredLogCheckCountsAFailedCallAsNotChecked(t *testing.T) {
	t.Parallel()

	chain := newTestAuditChain(t, audit.ChainOn)
	chain.entries = failingCheck{Store: chain.entries}
	chain.observed = metrics.NewServerMetrics("test", time.Now())

	chain.checkRestoredLog(t.Context(), []namespace.Name{audit.ServerScope})

	got := metricLine(t, chain)
	if !strings.Contains(got, "dustpan_audit_restore_check_failures_total 0") {
		t.Fatalf("metrics after a failed call:\n%s\nwant no failed restore check", got)
	}

	if !strings.Contains(got, "dustpan_audit_chain_check_errors_total 1") {
		t.Fatalf("metrics after a failed call:\n%s\nwant one check error", got)
	}
}

// TestChainRunnerClaimsBeatAndUseTheLease proves that the runner of the audit
// chain migration claims through the claim recorder: a worker with no job
// schedule that only polls for a migration still writes its heartbeat.
func TestChainRunnerClaimsBeatAndUseTheLease(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.auditChain = audit.ChainOn
	cfg.components = Components{api: false, worker: true}
	cfg.durableStore = st.DurableOperations()
	cfg.instanceID = "worker-1"
	cfg.operationLease = time.Minute
	cfg.watch = newWorkerWatch(cfg)

	chain, err := newAuditChain(cfg, nil)
	if err != nil || chain == nil {
		t.Fatalf("new audit chain = %v, %v", chain, err)
	}

	if beat, beatErr := st.DurableOperations().LastBeat(t.Context()); beatErr != nil || beat.Owner != "" {
		t.Fatalf("heartbeat before the claim = %+v, %v; want none", beat, beatErr)
	}

	if runErr := chain.runner.RunOnce(t.Context()); !errors.Is(runErr, durable.ErrNoWork) {
		t.Fatalf("run once = %v, want %v", runErr, durable.ErrNoWork)
	}

	beat, err := st.DurableOperations().LastBeat(t.Context())
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}

	if beat.Owner != "worker-1" {
		t.Fatalf("heartbeat owner after the claim = %q, want worker-1", beat.Owner)
	}
}
