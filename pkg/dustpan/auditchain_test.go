package dustpan_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
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
// hash, and records the state with no change entry.
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

	if got := listServer(t, st); len(got) != 0 {
		t.Fatalf("server scope: got %d entries, want no change entry", len(got))
	}
}

// TestAuditChainStaysOnWhenTheLogHoldsChainedEntries proves ADR 60 decision
// 19: an installation with chained entries keeps the chain on, and the
// scheduled check sets the metric.
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

	if got := listServer(t, st); len(got) != 0 {
		t.Fatalf("server scope: got %d entries, want no change entry", len(got))
	}
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

	server := listServer(t, st)
	if len(server) != 1 {
		t.Fatalf("server scope: got %d entries, want one change entry", len(server))
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
		got, err := st.ChainState(t.Context())
		if err != nil {
			t.Fatalf("chain state: %v", err)
		}

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
