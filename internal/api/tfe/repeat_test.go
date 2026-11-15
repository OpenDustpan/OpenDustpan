package tfe_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// The tokens of the repeat tests: bob holds the lock, alice forces a release.
const (
	repeatBobToken   = "repeat-bob"
	repeatAliceToken = "repeat-alice"
)

// repeatAuthenticator signs in bob and alice.
type repeatAuthenticator struct{}

func (repeatAuthenticator) AuthenticateToken(_ context.Context, raw string) (identity.Principal, error) {
	subject, known := map[string]string{repeatBobToken: "bob", repeatAliceToken: "alice"}[raw]
	if !known {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: subject, DisplayName: subject, Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		Kind: "", Credential: "", JobID: "",
	}, nil
}

// errRepeatNoSeal is the answer of repeatSealer. The repeat tests write no
// state, so nothing seals.
var errRepeatNoSeal = errors.New("the repeat tests seal nothing")

// repeatSealer is a workspace.Sealer that refuses each call.
type repeatSealer struct{}

func (repeatSealer) SealState(context.Context, namespace.Name, string, string, io.Reader) (io.Reader, error) {
	return nil, errRepeatNoSeal
}

func (repeatSealer) OpenState(context.Context, namespace.Name, string, string, io.Reader) (io.ReadCloser, error) {
	return nil, errRepeatNoSeal
}

func (repeatSealer) SealOutputs(context.Context, namespace.Name, string, string, []byte) ([]byte, error) {
	return nil, errRepeatNoSeal
}

func (repeatSealer) OpenOutputs(context.Context, namespace.Name, string, string, []byte) ([]byte, error) {
	return nil, errRepeatNoSeal
}

// repeatClock is a clock that a test moves.
type repeatClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *repeatClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *repeatClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// tfeRepeatEnv is a handler over a real workspace service on SQLite, with a
// clock that the test moves.
type tfeRepeatEnv struct {
	handler *tfe.Handler
	store   *sqlite.Store
	clock   *repeatClock
	ws      workspace.Workspace
}

// newTFERepeatEnv returns a handler over a real workspace service that holds
// the workspace acme/web, unlocked.
func newTFERepeatEnv(t *testing.T) tfeRepeatEnv {
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

	clock := &repeatClock{mu: sync.Mutex{}, now: time.Date(2026, time.November, 1, 9, 0, 0, 0, time.UTC)}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: clock.Now, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	acme, createErr := namespaces.Create(t.Context(), namespace.Name("acme"))
	if createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	workspaces, err := workspace.NewService(workspace.Config{
		BlobGuard: blobguardtest.None{}, ManualLocks: nil, Workspaces: store, States: store, Blobs: blobs,
		Namespaces: namespaces, OutOfScope: workspacetest.NewRefusals(), Sealer: repeatSealer{},
		Logger: slog.New(slog.DiscardHandler), Now: clock.Now, MaxStateBytes: 0, MaxPendingVersions: 0,
		Retention: 0,
	})
	if err != nil {
		t.Fatalf("workspaces: %v", err)
	}

	ws, err := workspaces.Create(
		t.Context(), workspace.Ref{Namespace: "acme", Name: "web"}, placementtest.Workspace(t, store, acme.Fence()),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	handler, err := tfe.NewHandler(tfe.Config{
		Registry: nil, Authenticator: repeatAuthenticator{}, Authorizer: allowAll(), AddressGate: nil,
		Logger: slog.New(slog.DiscardHandler), Namespaces: namespaces, Workspaces: workspaces,
		OutputChecker: newTestOutputChecker(t, allowAll(), workspaces), BaseURL: baseurl.Resolver{},
		Audit: newTestAudit(), Gate: nil, MaxStateBytes: 0, OnAuditFailure: nil, DenialLimiter: nil,
		Sessions: nil, SessionPolicy: nil, StateGuard: stategate.Off("the test keeps no gates"), Placer: testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return tfeRepeatEnv{handler: handler, store: store, clock: clock, ws: ws}
}

// tfeRepeatStep is one request of a repeat case: an action of the workspace,
// the token of the caller and the status that the step wants. advance moves
// the clock before the request.
type tfeRepeatStep struct {
	action  string
	token   string
	advance time.Duration
	want    int
}

// tfeStep returns a step that does not move the clock.
func tfeStep(action, token string, want int) tfeRepeatStep {
	return tfeRepeatStep{action: action, token: token, advance: 0, want: want}
}

// TestTFERepeatedLockActions proves the answer of a lock action that
// OpenTofu sends again after its answer is lost. Each case runs its steps on
// a new workspace. The last step is the request under test, and it changes
// nothing: the revision of the workspace and the audit entries stay as they
// were. A repeated lock stays 409: it is the accepted exception.
func TestTFERepeatedLockActions(t *testing.T) {
	t.Parallel()

	const lock, unlock, force = "lock", "unlock", "force-unlock"

	bob, alice := repeatBobToken, repeatAliceToken
	ok, conflict := http.StatusOK, http.StatusConflict

	cases := []struct {
		name  string
		steps []tfeRepeatStep
	}{
		{
			name:  "a repeated lock stays a conflict",
			steps: []tfeRepeatStep{tfeStep(lock, bob, ok), tfeStep(lock, bob, conflict)},
		},
		{
			name:  "an unlock by the holder of the record of a normal release succeeds",
			steps: []tfeRepeatStep{tfeStep(lock, bob, ok), tfeStep(unlock, bob, ok), tfeStep(unlock, bob, ok)},
		},
		{
			name: "an unlock by another caller is a conflict",
			steps: []tfeRepeatStep{
				tfeStep(lock, bob, ok), tfeStep(unlock, bob, ok), tfeStep(unlock, alice, conflict),
			},
		},
		{
			name: "the unlock of the original holder after a forced release is a conflict",
			steps: []tfeRepeatStep{
				tfeStep(lock, bob, ok), tfeStep(force, alice, ok), tfeStep(unlock, bob, conflict),
			},
		},
		{
			name:  "a repeated force-unlock by the same releaser succeeds",
			steps: []tfeRepeatStep{tfeStep(lock, bob, ok), tfeStep(force, alice, ok), tfeStep(force, alice, ok)},
		},
		{
			name: "a force-unlock by another caller after a forced release is a conflict",
			steps: []tfeRepeatStep{
				tfeStep(lock, bob, ok), tfeStep(force, alice, ok), tfeStep(force, bob, conflict),
			},
		},
		{
			name: "a force-unlock after a normal release is a conflict",
			steps: []tfeRepeatStep{
				tfeStep(lock, bob, ok), tfeStep(unlock, bob, ok), tfeStep(force, bob, conflict),
			},
		},
		{
			name: "an unlock that matches a record older than 24 hours is a conflict",
			steps: []tfeRepeatStep{
				tfeStep(lock, bob, ok), tfeStep(unlock, bob, ok),
				{action: unlock, token: bob, advance: workspace.ReleaseWindow, want: conflict},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := newTFERepeatEnv(t)
			last := len(tc.steps) - 1

			for i, step := range tc.steps[:last] {
				if got := env.send(t, step); got != step.want {
					t.Fatalf("step %d: %s: status %d, want %d", i, step.action, got, step.want)
				}
			}

			before := env.snapshot(t)

			step := tc.steps[last]
			if got := env.send(t, step); got != step.want {
				t.Fatalf("%s under test: status %d, want %d", step.action, got, step.want)
			}

			if after := env.snapshot(t); after != before {
				t.Fatalf("request under test changed the workspace: %+v, want %+v", after, before)
			}
		})
	}
}

// send moves the clock by step.advance and posts the action of step. It
// returns the status.
func (e tfeRepeatEnv) send(t *testing.T, step tfeRepeatStep) int {
	t.Helper()

	e.clock.advance(step.advance)

	target := "/api/v2/workspaces/" + e.ws.ID.String() + "/actions/" + step.action

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+step.token)
	req.Header.Set("Content-Type", "application/vnd.api+json")

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec.Code
}

// tfeRepeatSnapshot is what a request that changes nothing leaves as it was.
type tfeRepeatSnapshot struct {
	revision int64
	entries  int
}

// snapshot returns the revision of the workspace and the count of the audit
// entries of its namespace.
func (e tfeRepeatEnv) snapshot(t *testing.T) tfeRepeatSnapshot {
	t.Helper()

	ws, err := e.store.GetWorkspaceByID(t.Context(), e.ws.ID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}

	listing, err := e.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return tfeRepeatSnapshot{revision: ws.Revision, entries: len(listing.Entries)}
}
