package tofuhttp_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// errNoSeal is the answer of noSealer. The repeat tests write no state, so
// nothing seals.
var errNoSeal = errors.New("the repeat tests seal nothing")

// noSealer is a workspace.Sealer that refuses each call.
type noSealer struct{}

func (noSealer) SealState(context.Context, namespace.Name, string, string, io.Reader) (io.Reader, error) {
	return nil, errNoSeal
}

func (noSealer) OpenState(context.Context, namespace.Name, string, string, io.Reader) (io.ReadCloser, error) {
	return nil, errNoSeal
}

func (noSealer) SealOutputs(context.Context, namespace.Name, string, string, []byte) ([]byte, error) {
	return nil, errNoSeal
}

func (noSealer) OpenOutputs(context.Context, namespace.Name, string, string, []byte) ([]byte, error) {
	return nil, errNoSeal
}

// testClock is a clock that a test moves.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// repeatEnv is a handler over a real workspace service on SQLite, with a
// clock that the test moves.
type repeatEnv struct {
	handler http.Handler
	store   *sqlite.Store
	clock   *testClock
}

// newRepeatEnv returns a handler over a real workspace service, and the
// store and the clock behind it.
func newRepeatEnv(t *testing.T) repeatEnv {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	clock := &testClock{mu: sync.Mutex{}, now: time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC)}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: clock.Now, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	if _, createErr := namespaces.Create(t.Context(), namespace.Name("acme")); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		BlobGuard: blobguardtest.None{}, ManualLocks: nil, Workspaces: store, States: store, Blobs: blobs,
		Namespaces: namespaces, OutOfScope: workspacetest.NewRefusals(), Sealer: noSealer{},
		Logger: slog.New(slog.DiscardHandler), Now: clock.Now, MaxStateBytes: 0, MaxPendingVersions: 0,
		Retention: 0,
	})
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}

	// The workspace exists before the first LOCK, so bob, who may not create
	// it, can lock it.
	acme, err := namespaces.Get(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	place := placementtest.Workspace(t, store, acme.Fence())

	_, createErr := workspaces.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: "net"}, place)
	if createErr != nil {
		t.Fatalf("create workspace: %v", createErr)
	}

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  tokenAuth{},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     workspaces,
		Namespaces:     namespaces,
		Audit:          newTestAudit(),
		Gate:           nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
		OnAuditFailure: nil,
		DenialLimiter:  nil,
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return repeatEnv{handler: h, store: store, clock: clock}
}

// repeatStep is one request of a repeat case. bob holds the user token and
// may not force a release; alice holds the admin token and may.
type repeatStep struct {
	token  string
	method string
	lockID string
	// advance moves the clock before the request.
	advance time.Duration
	want    int
}

// lockStep and unlockStep return the LOCK and the UNLOCK of token under
// lockID, with the status that the step wants.
func lockStep(token, lockID string, want int) repeatStep {
	return repeatStep{token: token, method: "LOCK", lockID: lockID, advance: 0, want: want}
}

func unlockStep(token, lockID string, want int) repeatStep {
	return repeatStep{token: token, method: "UNLOCK", lockID: lockID, advance: 0, want: want}
}

// TestRepeatedLockRequests proves the answer of a LOCK or an UNLOCK that
// OpenTofu sends again after its answer is lost. Each case runs its steps on
// a new workspace. The last step is the request under test. When it succeeds,
// it changes nothing: the revision of the workspace and the audit entries
// stay as they were.
func TestRepeatedLockRequests(t *testing.T) {
	t.Parallel()

	const lockA, lockB = "lock-a", "lock-b"

	cases := []struct {
		name  string
		steps []repeatStep
	}{
		{
			name: "a LOCK with the same lock ID and holder succeeds",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), lockStep(userToken, lockA, http.StatusOK),
			},
		},
		{
			name: "a LOCK with another lock ID is locked",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), lockStep(userToken, lockB, http.StatusLocked),
			},
		},
		{
			name: "a LOCK with the same lock ID by another caller is locked",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), lockStep(adminToken, lockA, http.StatusLocked),
			},
		},
		{
			name: "an UNLOCK that matches the record of a normal release succeeds",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), unlockStep(userToken, lockA, http.StatusOK),
				unlockStep(userToken, lockA, http.StatusOK),
			},
		},
		{
			name: "an UNLOCK with another lock ID is a conflict",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), unlockStep(userToken, lockA, http.StatusOK),
				unlockStep(userToken, lockB, http.StatusConflict),
			},
		},
		{
			name: "an UNLOCK by another caller is a conflict",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), unlockStep(userToken, lockA, http.StatusOK),
				unlockStep(adminToken, lockA, http.StatusConflict),
			},
		},
		{
			name: "the UNLOCK of the original holder after a forced release is a conflict",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), unlockStep(adminToken, lockA, http.StatusOK),
				unlockStep(userToken, lockA, http.StatusConflict),
			},
		},
		{
			name: "a repeated force-unlock by the same releaser succeeds",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), unlockStep(adminToken, lockA, http.StatusOK),
				unlockStep(adminToken, lockA, http.StatusOK),
			},
		},
		{
			name: "an UNLOCK that matches a record older than 24 hours is a conflict",
			steps: []repeatStep{
				lockStep(userToken, lockA, http.StatusOK), unlockStep(userToken, lockA, http.StatusOK),
				{
					token: userToken, method: "UNLOCK", lockID: lockA, advance: workspace.ReleaseWindow,
					want: http.StatusConflict,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newRepeatEnv(t)
			last := len(tc.steps) - 1

			for i, step := range tc.steps[:last] {
				if rec := env.send(step); rec != step.want {
					t.Fatalf("step %d: %s: status %d, want %d", i, step.method, rec, step.want)
				}
			}

			before := env.snapshot(t)

			step := tc.steps[last]
			if got := env.send(step); got != step.want {
				t.Fatalf("%s under test: status %d, want %d", step.method, got, step.want)
			}

			if after := env.snapshot(t); after != before {
				t.Fatalf("request under test changed the workspace: %+v, want %+v", after, before)
			}
		})
	}
}

// send moves the clock by step.advance and sends the request of step. It
// returns the status.
func (e repeatEnv) send(step repeatStep) int {
	e.clock.advance(step.advance)

	return do(e.handler, step.method, unitPath, lockBody(step.lockID, "tofu"), bearer(step.token)).Code
}

// repeatSnapshot is what a request that changes nothing leaves as it was.
type repeatSnapshot struct {
	revision int64
	entries  int
}

// snapshot returns the revision of the workspace and the count of the audit
// entries of its namespace.
func (e repeatEnv) snapshot(t *testing.T) repeatSnapshot {
	t.Helper()

	ws, err := e.store.GetWorkspace(t.Context(), workspace.Ref{Namespace: "acme", Name: "net"})
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	listing, err := e.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return repeatSnapshot{revision: ws.Revision, entries: len(listing.Entries)}
}
