package dustpan

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// sweepChainServer answers a Server with the chain, the deleted-unit
// sweeper and the token service of a test. Logs go to log at DEBUG.
func sweepChainServer(
	t *testing.T, log *bytes.Buffer, chain *auditChain, deleted *deletedSweeper, tokens *accesstoken.Service,
) *Server {
	t.Helper()

	return &Server{
		kind:             kindState{store: nil, principals: nil, kind: ""},
		instances:        instanceState{store: nil, id: "", version: "", registered: nil},
		ready:            nil,
		drainPeriod:      0,
		tempAdmins:       nil,
		manualLocks:      nil,
		reconciler:       nil,
		artifactSweeper:  nil,
		nameLocks:        nameLockPrune{locks: nil},
		issuer:           nil,
		handler:          nil,
		started:          nil,
		ungated:          nil,
		mounted:          nil,
		root:             nil,
		logger:           slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		listenAddr:       "",
		listener:         nil,
		secrets:          nil,
		secretKeys:       nil,
		secretWrapper:    nil,
		workspaces:       nil,
		backups:          nil,
		restores:         nil,
		storeChecks:      nil,
		now:              nil,
		registry:         nil,
		tlsCertFile:      "",
		tlsKeyFile:       "",
		observed:         nil,
		chain:            chain,
		tokens:           tokens,
		shipper:          nil,
		beforeServe:      nil,
		deleted:          deleted,
		signInRequests:   nil,
		signInPruneEvery: 0,
		signInProviders:  providerBinding{store: nil, providers: nil},
		durable:          nil,
		components:       AllComponents(),
		watch:            nil,
		durableOwner:     "",
		inactivityEvery:  0,
		deletedEvery:     0,
		idempotency:      nil,
		idempotencyEvery: 0,
		auditChainEvery:  0,
		flushDenials:     nil,
		stateWrite: stateWriteStart{
			effective: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
			audit:     nil,
		},
	}
}

// clearChainWait stores the record of chain with no wait.
func clearChainWait(t *testing.T, chain *auditChain) {
	t.Helper()

	rec, err := chain.store.ChainRecord(t.Context())
	if err != nil {
		t.Fatalf("chain record: %v", err)
	}

	rec.Pending = false

	if err := chain.store.SaveChainRecord(t.Context(), rec); err != nil {
		t.Fatalf("save chain record: %v", err)
	}
}

// TestChainChangeWaitsWhileWaiting proves the sweeps skip one run while a
// change of the chain waits.
func TestChainChangeWaitsWhileWaiting(t *testing.T) {
	t.Parallel()

	log := &bytes.Buffer{}
	srv := sweepChainServer(t, log, newTestAuditChain(t, audit.ChainOn), nil, nil)

	if !srv.chainChangeWaits(t.Context()) {
		t.Fatal("chain change waits: got false, want true")
	}

	if !strings.Contains(log.String(), "an audit chain change waits") {
		t.Fatalf("log misses the skip line:\n%s", log.String())
	}
}

// TestChainChangeWaitsWhenSteady proves the sweeps run when no change of
// the chain waits.
func TestChainChangeWaitsWhenSteady(t *testing.T) {
	t.Parallel()

	log := &bytes.Buffer{}
	chain := newTestAuditChain(t, audit.ChainOn)
	clearChainWait(t, chain)

	srv := sweepChainServer(t, log, chain, nil, nil)

	if srv.chainChangeWaits(t.Context()) {
		t.Fatal("steady chain: got true, want false")
	}

	if strings.Contains(log.String(), "skipping the sweep") {
		t.Fatalf("log holds a skip line:\n%s", log.String())
	}
}

// TestChainChangeWaitsWithoutChain proves a server without a chain runs.
func TestChainChangeWaitsWithoutChain(t *testing.T) {
	t.Parallel()

	log := &bytes.Buffer{}
	srv := sweepChainServer(t, log, nil, nil, nil)

	if srv.chainChangeWaits(t.Context()) {
		t.Fatal("server without a chain: got true, want false")
	}
}

// TestChainChangeWaitsWhenUnknown proves the sweeps skip one run when the
// chain state cannot be read, with a warning.
func TestChainChangeWaitsWhenUnknown(t *testing.T) {
	t.Parallel()

	log := &bytes.Buffer{}
	chain := newTestAuditChain(t, audit.ChainOn)

	closer, ok := chain.store.(interface{ Close() error })
	if !ok {
		t.Fatal("chain store cannot close")
	}

	if err := closer.Close(); err != nil {
		t.Fatalf("close chain store: %v", err)
	}

	srv := sweepChainServer(t, log, chain, nil, nil)

	if !srv.chainChangeWaits(t.Context()) {
		t.Fatal("unknown chain state: got false, want true")
	}

	if !strings.Contains(log.String(), "the audit chain state is unknown") {
		t.Fatalf("log misses the warning:\n%s", log.String())
	}
}

// TestSweepDeletedOnceSkipsWhileChainChangeWaits proves the retention sweep
// skips one run while a change of the chain waits: due units stay. The run
// after the wait clears purges them.
func TestSweepDeletedOnceSkipsWhileChainChangeWaits(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	units := f.deleteUnits(t)
	f.clock.advance(2 * time.Hour)

	log := &bytes.Buffer{}
	chain := newTestAuditChain(t, audit.ChainOn)
	srv := sweepChainServer(t, log, chain, f.sweeper, nil)

	srv.sweepDeletedOnce(t.Context())

	for _, u := range units {
		if n := f.blobCount(t, u.prefix); n != 1 {
			t.Fatalf("blobs of %s while the change waits = %d, want 1", u.id, n)
		}
	}

	if !strings.Contains(log.String(), "an audit chain change waits") {
		t.Fatalf("log misses the skip line:\n%s", log.String())
	}

	clearChainWait(t, chain)
	srv.sweepDeletedOnce(t.Context())

	for _, u := range units {
		if n := f.blobCount(t, u.prefix); n != 0 {
			t.Fatalf("blobs of %s after the wait clears = %d, want 0", u.id, n)
		}
	}
}

// TestSweepOnceSkipsWhileChainChangeWaits proves the inactivity sweep skips
// one run while a change of the chain waits. The token service sits on a
// closed store, so a run would fail loudly: only the skip line may appear.
func TestSweepOnceSkipsWhileChainChangeWaits(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      st, Principals: st, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: nil,
		NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	log := &bytes.Buffer{}
	srv := sweepChainServer(t, log, newTestAuditChain(t, audit.ChainOn), nil, tokens)

	srv.sweepOnce(t.Context())

	logged := log.String()
	if !strings.Contains(logged, "an audit chain change waits") {
		t.Fatalf("log misses the skip line:\n%s", logged)
	}

	if strings.Contains(logged, "inactivity disable failed") {
		t.Fatalf("the sweep ran while the change waits:\n%s", logged)
	}
}
