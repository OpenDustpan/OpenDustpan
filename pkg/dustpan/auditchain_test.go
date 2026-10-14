package dustpan_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// chainWait bounds how long a test waits for the server to start.
const chainWait = 10 * time.Second

// TestAuditChainOnANewInstallationStartsOff proves ADR 60 decision 17: a
// new installation with the default setting appends entries without a
// hash. Its first start writes one switch entry without a hash for the
// start state (ADR 64).
func TestAuditChainOnANewInstallationStartsOff(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)

	stop := runChainServer(t, st, audit.ChainAuto)
	waitForChainState(t, st, audit.ChainStateOff)
	stop()

	if e := appendChainEntry(t, st, acme, "req-1"); e.Hash != "" {
		t.Fatalf("entry on a new installation: hash %q, want none", e.Hash)
	}

	checkStartSwitch(t, st, startSwitch{path: "/audit/chain/off", chained: false})
}

// TestAuditChainStaysOnWhenTheLogHoldsChainedEntries proves ADR 60 decision
// 19: an installation with chained entries keeps the chain on, writes one
// chained switch entry for the start state (ADR 64), and the scheduled
// check sets the metric.
func TestAuditChainStaysOnWhenTheLogHoldsChainedEntries(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)

	// A store handle computes the chain until the server sets it, as every
	// installation before ADR 60 did.
	appendChainEntry(t, st, acme, "req-1")

	srv, stop := runChainServerWithMetrics(t, st, audit.ChainAuto)
	waitForChainState(t, st, audit.ChainStateOn)
	waitForMetricLine(t, srv, "dustpan_audit_chain_invalid 0")
	stop()

	if e := appendChainEntry(t, st, acme, "req-2"); e.Hash == "" {
		t.Fatal("entry after the start: no hash, want the chain on")
	}

	checkStartSwitch(t, st, startSwitch{path: "/audit/chain/on", chained: true})
}

// TestTurningTheAuditChainOffIsAudited proves the lead decision recorded in
// RFC 22: an administrator who turns the chain off on purpose makes one
// chained change entry in the server scope, and later entries hold no hash.
func TestTurningTheAuditChainOffIsAudited(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	stop := runChainServer(t, st, audit.ChainOff)
	waitForChainState(t, st, audit.ChainStateOff)
	stop()

	// The list answers newest first: the change after the start switch.
	server := listServer(t, st)
	if len(server) != 2 {
		t.Fatalf("server scope: got %d entries, want the start switch and one change", len(server))
	}

	change := server[0]
	if change.Kind != audit.KindChange || change.Action != audit.ActionSetChain ||
		change.ValuePath != "/audit/chain/off" || change.Hash == "" {
		t.Fatalf("change entry: got %+v, want a chained change that turns the chain off", change)
	}

	if e := appendChainEntry(t, st, acme, "req-2"); e.Hash != "" {
		t.Fatalf("entry after the chain went off: hash %q, want none", e.Hash)
	}
}

// TestAStoppedRestoreRechecksTheAuditChain proves ADR 64 decision 6 for a
// restore that stops after its swap: the next start writes the switch entry
// of the restore, which names the requester and the backup of the mark.
func TestAStoppedRestoreRechecksTheAuditChain(t *testing.T) {
	t.Parallel()

	st, stopped := stoppedRestore(t, true)

	before := len(listServer(t, st))

	stop := runChainServer(t, st, audit.ChainOn)
	waitForServerEntries(t, st, before+1)
	waitForRestoreState(t, st, stopped.ID, backup.RestoreFailed)
	stop()

	// The list answers newest first.
	got := listServer(t, st)[0]
	marked := len(got.Changes) == 1 && got.Changes[0].Key == audit.ChangeRestore &&
		got.Changes[0].New == stopped.BackupID.String()

	if got.Action != audit.ActionSetChain || got.Subject != "alice" || got.Hash == "" || !marked {
		t.Fatalf("restore switch entry: got %+v, want a chained switch by alice that marks the restore", got)
	}
}

// TestARestoreStoppedBeforeItsSwapWritesNoSwitchEntry proves that a restore
// that stopped before its swap writes no switch entry at the next start:
// the log is still the log of this installation.
func TestARestoreStoppedBeforeItsSwapWritesNoSwitchEntry(t *testing.T) {
	t.Parallel()

	st, stopped := stoppedRestore(t, false)

	before := len(listServer(t, st))

	stop := runChainServer(t, st, audit.ChainOn)
	waitForRestoreState(t, st, stopped.ID, backup.RestoreFailed)
	stop()

	if got := len(listServer(t, st)); got != before {
		t.Fatalf("server scope after the start: got %d entries, want %d", got, before)
	}
}

// stoppedRestore answers a store with the chain on and a restore that
// stopped while it applied, with the mark of the restore in the chain
// record. swapped says whether the swap replaced the log before the stop.
func stoppedRestore(t *testing.T, swapped bool) (*sqlite.Store, backup.RestoreRecord) {
	t.Helper()

	st := openChainStore(t)
	createChainNamespace(t, st)

	stop := runChainServer(t, st, audit.ChainOn)
	waitForChainState(t, st, audit.ChainStateOn)
	stop()

	stopped := backup.RestoreRecord{
		StartedAt:   time.Now().UTC(),
		EndedAt:     time.Time{},
		ID:          backup.RestoreID("rst-chain0000000000001"),
		BackupID:    backup.ID("bkp-chain0000000000001"),
		PreBackupID: "",
		RequestedBy: "alice",
		Error:       "",
		State:       backup.RestoreChecking,
	}

	if err := st.CreateRestore(t.Context(), stopped); err != nil {
		t.Fatalf("create restore: %v", err)
	}

	if err := st.MarkRestoreApplying(t.Context(), stopped.ID, ""); err != nil {
		t.Fatalf("mark restore applying: %v", err)
	}

	rec, err := st.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	// The record holds the mark that the server writes before the swap,
	// and the swap sets Applied.
	next := rec
	next.Restore = audit.ChainRestoreMark{
		ID:          stopped.ID.String(),
		BackupID:    stopped.BackupID.String(),
		RequestedBy: stopped.RequestedBy,
		Applied:     swapped,
	}

	w := audit.ChainWrite{Now: nil, Entries: nil, Read: rec, Record: next}
	if err := st.RecordChain(t.Context(), w); err != nil {
		t.Fatalf("mark the restore: %v", err)
	}

	return st, stopped
}

// waitForRestoreState waits until the restore id in st holds want.
func waitForRestoreState(t *testing.T, st *sqlite.Store, id backup.RestoreID, want backup.RestoreState) {
	t.Helper()

	deadline := time.Now().Add(chainWait)

	for {
		got, err := st.GetRestore(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore %s: %v", id, err)
		}

		if got.State == want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("restore %s after %s: state %q, want %q", id, chainWait, got.State, want)
		}

		pauseChain(t)
	}
}

// waitForServerEntries waits until the server scope of st holds want
// entries.
func waitForServerEntries(t *testing.T, st *sqlite.Store, want int) {
	t.Helper()

	deadline := time.Now().Add(chainWait)

	for len(listServer(t, st)) < want {
		if time.Now().After(deadline) {
			t.Fatalf("server scope after %s: got %d entries, want %d", chainWait, len(listServer(t, st)), want)
		}

		pauseChain(t)
	}
}

// startSwitch is the switch entry of a first start: its value path and
// whether it holds a hash.
type startSwitch struct {
	path    string
	chained bool
}

// checkStartSwitch proves that the server scope of st holds only the switch
// entry want.
func checkStartSwitch(t *testing.T, st *sqlite.Store, want startSwitch) {
	t.Helper()

	server := listServer(t, st)
	if len(server) != 1 {
		t.Fatalf("server scope: got %d entries, want the start switch only", len(server))
	}

	got := startSwitch{path: server[0].ValuePath, chained: server[0].Hash != ""}
	if server[0].Action != audit.ActionSetChain || got != want {
		t.Fatalf("start switch: got %+v, want %+v", got, want)
	}
}

// openChainStore opens a fresh SQLite store that the test closes.
func openChainStore(t *testing.T) *sqlite.Store {
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

	return st
}

// createChainNamespace creates the namespace acme in st.
func createChainNamespace(t *testing.T, st *sqlite.Store) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
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

// appendChainEntry appends one reveal entry to ns and answers it as stored.
func appendChainEntry(t *testing.T, st *sqlite.Store, ns namespace.Namespace, requestID string) audit.Entry {
	t.Helper()

	stored, err := st.Append(t.Context(), ns.Fence(), audit.Entry{
		At:         time.Now(),
		Namespace:  ns.Name,
		Subject:    "admin",
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:read-secrets",
		Object:     "opentofu:acme/prod",
		Version:    "sv-1",
		ValuePath:  "/outputs/db",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "test",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindReveal,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  requestID,
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	})
	if err != nil {
		t.Fatalf("append audit entry: %v", err)
	}

	return stored
}

// listServer answers the entries of the server scope.
func listServer(t *testing.T, st *sqlite.Store) []audit.Entry {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list server scope: %v", err)
	}

	return listing.Entries
}

// runChainServer starts a server on st with setting and answers a function
// that stops it and waits for Run.
func runChainServer(t *testing.T, st *sqlite.Store, setting audit.ChainSetting) func() {
	t.Helper()

	_, stop := runChainServerWithMetrics(t, st, setting)

	return stop
}

// runChainServerWithMetrics starts a server on st with setting and answers
// it and a function that stops it and waits for Run.
func runChainServerWithMetrics(t *testing.T, st *sqlite.Store, setting audit.ChainSetting) (*dustpan.Server, func()) {
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

	srv, err := dustpan.NewServer(
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
		dustpan.WithListener(openTestListener(t)),
		dustpan.WithAuditChain(setting),
	)
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

	return srv, stop
}

// waitForChainState waits until st records want, which Run does during its
// start-up.
func waitForChainState(t *testing.T, st *sqlite.Store, want audit.ChainState) {
	t.Helper()

	deadline := time.Now().Add(chainWait)

	for {
		rec, err := st.ChainRecord(t.Context())
		if err != nil {
			t.Fatalf("chain state: %v", err)
		}

		got := rec.State
		if got == want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("chain state after %s: got %q, want %q", chainWait, got, want)
		}

		pauseChain(t)
	}
}

// waitForMetricLine waits until the metrics output of srv holds want.
func waitForMetricLine(t *testing.T, srv *dustpan.Server, want string) {
	t.Helper()

	deadline := time.Now().Add(chainWait)

	for {
		var out strings.Builder
		if err := srv.Metrics().Write(&out); err != nil {
			t.Fatalf("write metrics: %v", err)
		}

		if strings.Contains(out.String(), want) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("metrics lack %q after %s", want, chainWait)
		}

		pauseChain(t)
	}
}

// pauseChain waits a short time, or fails the test when it ends first.
func pauseChain(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("test ended while it waited for the server")
	}
}
