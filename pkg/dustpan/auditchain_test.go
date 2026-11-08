package dustpan_test

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// chainWait bounds how long a test waits for the server. A migration waits
// four cache limits of maintenance mode, 8 seconds.
const chainWait = 30 * time.Second

// TestAuditChainIsOnByDefault proves that a new installation keeps the
// chain on with the default setting, writes no entry at the start, and that
// the scheduled check sets the metric.
func TestAuditChainIsOnByDefault(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)

	srv := runChainServer(t, st, audit.ChainOn)
	waitForMetricLine(t, srv.srv, "dustpan_audit_chain_invalid 0")

	if e := appendChainEntry(t, st, acme, "req-1"); e.Hash == "" {
		t.Fatal("entry with the default setting: no hash, want the chain on")
	}

	if got := listServer(t, st); len(got) != 0 {
		t.Fatalf("server scope after a start: got %d entries, want none", len(got))
	}

	if status, body := srv.do(t, http.MethodGet, "/api/stacks"); status == http.StatusServiceUnavailable {
		t.Fatalf("tool API with the setting of the stored state: 503, want it served (%s)", body)
	}
}

// TestAuditChainChangeWaitsForApprovalAndMigrates proves the change of the
// chain: a start with the setting off waits in maintenance mode, the
// approval writes an entry and an end entry in each scope, and a later
// start with the setting on writes a start entry with the digest of the
// earlier entries.
func TestAuditChainChangeWaitsForApprovalAndMigrates(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	off := runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })
	wantMaintenance(t, st, maintenance.StateMaintenance)
	wantWaitingChange(t, off)
	approveAndWait(t, st, off, audit.ChainStateOff)

	entries := listScope(t, st, acme.Name)
	if len(entries) != 2 || entries[0].Action != audit.ActionEndChain || entries[0].Subject != "admin" ||
		entries[0].Hash == "" || entries[0].PrevHash != entries[1].Hash {
		t.Fatalf("acme after the change to off: got %+v, want an end entry by admin linked to the chain", entries)
	}

	if e := appendChainEntry(t, st, acme, "req-2"); e.Hash != "" {
		t.Fatalf("entry after the change to off: hash %q, want none", e.Hash)
	}

	on := runChainServer(t, st, audit.ChainOn)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })
	approveAndWait(t, st, on, audit.ChainStateOn)

	start := listScope(t, st, acme.Name)[0]
	if start.Action != audit.ActionStartChain || start.PrevHash != "" || start.Hash == "" || len(start.Changes) != 2 {
		t.Fatalf("acme after the change to on: got %+v, want a start entry with the digest", start)
	}

	wantValid(t, st, acme.Name, audit.ServerScope)
}

// TestRestartWithStoredSettingCancelsWaitingChange proves that a restart
// with the stored setting cancels a waiting change: a start with the
// setting off waits, and the next start with the setting on clears the
// wait and leaves maintenance mode.
func TestRestartWithStoredSettingCancelsWaitingChange(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	off := runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })
	waitForMaintenance(t, st, maintenance.StateMaintenance)
	off.stop()

	runChainServer(t, st, audit.ChainOn)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return !rec.Pending })
	waitForMaintenance(t, st, maintenance.StateNormal)
}

// TestRestartWithStoredSettingKeepsOperatorMaintenance proves that a restart
// with the stored setting keeps the maintenance mode of an operator: the
// operator enters maintenance mode, a start with the setting off waits
// without entering it again, and the next start with the setting on clears
// the wait but stays in maintenance mode.
func TestRestartWithStoredSettingKeepsOperatorMaintenance(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	on := runChainServer(t, st, audit.ChainOn)
	if status, body := on.do(t, http.MethodPost, "/api/dustpan/v1/maintenance/enter"); status != http.StatusOK {
		t.Fatalf("enter maintenance mode: %d %s, want 200", status, body)
	}

	waitForMaintenance(t, st, maintenance.StateMaintenance)
	on.stop()

	off := runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending })

	if rec, err := st.ChainRecord(t.Context()); err != nil || rec.EnteredMaintenance {
		t.Fatalf("chain record = %+v, %v; want no entered maintenance", rec, err)
	}

	wantMaintenance(t, st, maintenance.StateMaintenance)
	off.stop()

	runChainServer(t, st, audit.ChainOn)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return !rec.Pending })
	wantMaintenance(t, st, maintenance.StateMaintenance)
}

// wantWaitingChange proves that c, whose setting is off and differs from the
// stored state, refuses the tool API and answers a waiting change.
func wantWaitingChange(t *testing.T, c chainServer) {
	t.Helper()

	if status, _ := c.do(t, http.MethodGet, "/api/stacks"); status != http.StatusServiceUnavailable {
		t.Fatalf("tool API while the setting differs: %d, want 503", status)
	}

	status, body := c.do(t, http.MethodGet, "/api/server/audit/chain")
	if status != http.StatusOK || !strings.Contains(body, `"pending":true`) ||
		!strings.Contains(body, `"setting":"off"`) {
		t.Fatalf("chain status: %d %s, want 200 with a waiting change to off", status, body)
	}
}

// approveAndWait approves the waiting change through c, waits until st
// holds state with no waiting change and normal operation, and stops c.
func approveAndWait(t *testing.T, st *sqlite.Store, c chainServer, state audit.ChainState) {
	t.Helper()

	if status, body := c.do(t, http.MethodPost, "/api/server/audit/chain"); status != http.StatusAccepted {
		t.Fatalf("approve %s: %d %s, want 202", state, status, body)
	}

	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.State == state && !rec.Pending })
	waitForMaintenance(t, st, maintenance.StateNormal)
	c.stop()
}

// TestAuditChainMismatchAfterAnotherApproval proves that an instance whose
// setting differs from the stored state refuses the tool APIs once another
// instance approved the change, until it restarts.
func TestAuditChainMismatchAfterAnotherApproval(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	createChainNamespace(t, st)

	on := runChainServer(t, st, audit.ChainOn)

	rec, err := st.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.State = audit.ChainStateOff
	saveRecord(t, st, rec)

	deadline := time.Now().Add(chainWait)

	for {
		status, _ := on.do(t, http.MethodGet, "/api/stacks")
		if status == http.StatusServiceUnavailable {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("tool API after the stored state changed: %d, want 503", status)
		}

		pauseChain(t)
	}

	for _, path := range []string{
		"/health/live", "/api/server/audit/chain", "/api/dustpan/v1/maintenance",
	} {
		if status, body := on.do(t, http.MethodGet, path); status == http.StatusServiceUnavailable {
			t.Fatalf("%s while the setting differs: 503, want it served (%s)", path, body)
		}
	}
}

// TestARestoredLogThatDisagreesWaitsForApproval proves that a start after a
// swap that replaced the log compares the restored log with the stored
// state: a log that does not agree waits for an approval.
func TestARestoredLogThatDisagreesWaitsForApproval(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	saveRecord(t, st, audit.ChainRecord{
		ChangedAt: time.Now(), ChangedBy: "alice", State: audit.ChainStateOff,
		Pending: false, EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: true,
	})

	runChainServer(t, st, audit.ChainOff)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return rec.Pending && !rec.LogReplaced })
	wantMaintenance(t, st, maintenance.StateMaintenance)
}

// TestARestoredLogThatAgreesStartsNormally proves that a restored log that
// agrees with the stored state clears the note of the swap and waits for
// nothing.
func TestARestoredLogThatAgreesStartsNormally(t *testing.T) {
	t.Parallel()

	st := openChainStore(t)
	acme := createChainNamespace(t, st)
	appendChainEntry(t, st, acme, "req-1")

	saveRecord(t, st, audit.ChainRecord{
		ChangedAt: time.Now(), ChangedBy: "", State: audit.ChainStateOn,
		Pending: false, EnteredMaintenance: false, MigratingAt: time.Time{}, LogReplaced: true,
	})

	srv := runChainServer(t, st, audit.ChainOn)
	waitForRecord(t, st, func(rec audit.ChainRecord) bool { return !rec.LogReplaced })
	waitForMetricLine(t, srv.srv, "dustpan_audit_restore_check_failures_total 0")

	rec, err := st.ChainRecord(t.Context())
	if err != nil || rec.Pending {
		t.Fatalf("chain record = %+v, %v; want no waiting change", rec, err)
	}

	wantMaintenance(t, st, maintenance.StateNormal)
}

// chainServer is a running server of a chain test.
type chainServer struct {
	srv  *dustpan.Server
	stop func()
	base string
}

// do sends one request with the admin token and answers the status and the
// body.
func (c chainServer) do(t *testing.T, method, path string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, c.base+path, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	authorize(t, req, adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, string(body)
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
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                "acme",
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
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

// listServer answers the entries of the server scope, newest first.
func listServer(t *testing.T, st *sqlite.Store) []audit.Entry {
	t.Helper()

	return listScope(t, st, audit.ServerScope)
}

// listScope answers the entries of scope, newest first.
func listScope(t *testing.T, st *sqlite.Store, scope namespace.Name) []audit.Entry {
	t.Helper()

	listing, err := st.List(t.Context(), scope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list %s: %v", scope, err)
	}

	// The setup of the console session of a test writes entries of its own.
	return slices.DeleteFunc(slices.Clone(listing.Entries), func(e audit.Entry) bool {
		return e.Subject == consoleSetupActor
	})
}

// saveRecord stores rec as the chain record of st.
func saveRecord(t *testing.T, st *sqlite.Store, rec audit.ChainRecord) {
	t.Helper()

	if err := st.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}
}

// wantValid proves that the chain check of each scope passes.
func wantValid(t *testing.T, st *sqlite.Store, scopes ...namespace.Name) {
	t.Helper()

	for _, scope := range scopes {
		got, err := st.Check(t.Context(), scope)
		if err != nil || !got.Valid {
			t.Fatalf("check %s = %+v, %v; want valid", scope, got, err)
		}
	}
}

// wantMaintenance proves that st holds the installation state want.
func wantMaintenance(t *testing.T, st *sqlite.Store, want maintenance.State) {
	t.Helper()

	got, err := st.GetState(t.Context())
	if err != nil || got != want {
		t.Fatalf("installation state = %q, %v; want %q", got, err, want)
	}
}

// waitForMaintenance waits until st holds the installation state want.
func waitForMaintenance(t *testing.T, st *sqlite.Store, want maintenance.State) {
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

// runChainServer starts a server on st with setting, waits until it
// serves, and answers it. The test stops it at its end.
func runChainServer(
	t *testing.T, st *sqlite.Store, setting audit.ChainSetting, extra ...dustpan.Option,
) chainServer {
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

	srv, err := dustpan.NewServer(append(append([]dustpan.Option{
		dustpan.WithInstallation(installationName),
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
		dustpan.WithAuditChain(setting),
	}, signInOptions(t, st)...), extra...)...)
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

// waitForLive waits until the liveness probe of c answers.
func waitForLive(t *testing.T, c chainServer) {
	t.Helper()

	deadline := time.Now().Add(chainWait)

	for {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.base+"/health/live", http.NoBody)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Errorf("close body: %v", closeErr)
			}

			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("server did not serve after %s: %v", chainWait, err)
		}

		pauseChain(t)
	}
}

// waitForRecord waits until the chain record of st meets ok.
func waitForRecord(t *testing.T, st *sqlite.Store, ok func(audit.ChainRecord) bool) {
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
		t.Fatal("test context ended")
	}
}
