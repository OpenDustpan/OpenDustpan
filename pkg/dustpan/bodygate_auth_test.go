package dustpan_test

import (
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// authGateTracker records whether a request body was read at all. It answers
// EOF at once, so a server that reads gets an empty body.
type authGateTracker struct {
	read atomic.Bool
}

func newTracker() *authGateTracker { return &authGateTracker{read: atomic.Bool{}} }

func (t *authGateTracker) Read(_ []byte) (int, error) {
	t.read.Store(true)

	return 0, io.EOF
}

// newFullGateFixture returns a server whose body budget holds 1 byte, so
// every large body meets a full gate. Any route that reserves before it
// authenticates answers 503 to the requests below.
func newFullGateFixture(t *testing.T) fixture {
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

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithMaxBodyBytesInFlight(1),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return fixture{store: st, server: nil, srv: srv}
}

// authFixtureIDs holds the identifiers the request tables need.
type authFixtureIDs struct {
	wsID  string
	svID  string
	wrong string
}

// prepareAuthFixture creates a namespace, a locked workspace and a pending
// state version, and mints one unknown upload capability.
func prepareAuthFixture(t *testing.T, f fixture) authFixtureIDs {
	t.Helper()

	f.createNamespace(t, "acme", installationName)

	ctx := t.Context()

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	ws, err := f.srv.Workspaces().Create(
		ctx, workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, f.store, "acme"),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if _, lockErr := f.srv.Workspaces().Lock(ctx, ws.ID, "admin", "apply"); lockErr != nil {
		t.Fatalf("lock workspace: %v", lockErr)
	}

	sum := md5.Sum([]byte(`{"version":4}`)) //nolint:gosec // The TFE protocol names state by MD5.

	pending, err := f.srv.Workspaces().BeginState(
		ctx,
		ws.ID,
		"admin",
		workspace.NewState{Lineage: "l1", MD5: hex.EncodeToString(sum[:]), Outputs: nil, Serial: 1, Force: false},
		workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil},
	)
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	bad, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	wrong := bad.Reveal()
	svID := pending.Version.ID.String()
	wsID := ws.ID.String()

	return authFixtureIDs{wsID: wsID, svID: svID, wrong: wrong}
}

// TestNoRouteReservesBeforeAuthentication proves no route that reserves body
// budget does so before it authenticates: with a gate that is always full,
// every such route answers something other than 503 to a request without
// credentials, and never reads its body. The control requests prove the gate
// is wired: the same server answers 503 to an authenticated large body on one
// route of each adapter.
func TestNoRouteReservesBeforeAuthentication(t *testing.T) {
	t.Parallel()

	f := newFullGateFixture(t)
	ids := prepareAuthFixture(t, f)
	ctx := t.Context()
	wsID, svID, wrong := ids.wsID, ids.svID, ids.wrong

	handler := startedHandler(t, f.srv)

	for _, tc := range unauthenticatedCases(wsID, svID, wrong) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracker := newTracker()

			req := httptest.NewRequestWithContext(ctx, tc.method, tc.target, tracker)
			req.ContentLength = 1 << 20

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusServiceUnavailable {
				t.Errorf(
					"%s %s without credentials answers 503: it reserves before it authenticates",
					tc.method,
					tc.target,
				)
			}

			if tracker.read.Load() {
				t.Errorf("%s %s without credentials read its body before authentication", tc.method, tc.target)
			}
		})
	}

	for _, tc := range controlCases(wsID) {
		t.Run("control "+tc.name, func(t *testing.T) {
			t.Parallel()

			tracker := newTracker()

			req := httptest.NewRequestWithContext(ctx, tc.method, tc.target, tracker)
			req.ContentLength = 1 << 20
			req.Header.Set("Authorization", tc.header)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf(
					"%s %s with credentials answers %d, want 503: the gate is not wired",
					tc.method,
					tc.target,
					rec.Code,
				)
			}
		})
	}
}

// routeCase is one request sent without credentials.
type routeCase struct {
	name   string
	method string
	target string
}

// controlCase is one request sent with a valid credential.
type controlCase struct {
	name   string
	method string
	target string
	header string
}

// unauthenticatedCases lists every route that reserves body budget.
func unauthenticatedCases(wsID, svID, wrong string) []routeCase {
	return []routeCase{
		// Every Pulumi route that reserves budget, without credentials.
		{
			name:   "journal entries",
			method: http.MethodPatch,
			target: "/api/stacks/acme/proj/stk/update/upd-test0001/journalentries",
		},
		{name: "reveal", method: http.MethodPost, target: "/api/stacks/acme/proj/stk/state/latest/reveal"},
		{name: "encrypt", method: http.MethodPost, target: "/api/stacks/acme/proj/stk/encrypt"},
		{name: "batch encrypt", method: http.MethodPost, target: "/api/stacks/acme/proj/stk/batch-encrypt"},
		{name: "decrypt", method: http.MethodPost, target: "/api/stacks/acme/proj/stk/decrypt"},
		{name: "batch decrypt", method: http.MethodPost, target: "/api/stacks/acme/proj/stk/batch-decrypt"},
		{name: "log decryption", method: http.MethodPost, target: "/api/stacks/acme/proj/stk/decrypt/log-decryption"},
		{
			name:   "log batch decryption",
			method: http.MethodPost,
			target: "/api/stacks/acme/proj/stk/decrypt/log-batch-decryption",
		},
		{
			name:   "checkpoint",
			method: http.MethodPatch,
			target: "/api/stacks/acme/proj/stk/update/upd-test0001/checkpoint",
		},
		{
			name:   "checkpoint verbatim",
			method: http.MethodPatch,
			target: "/api/stacks/acme/proj/stk/update/upd-test0001/checkpointverbatim",
		},
		{
			name:   "checkpoint delta",
			method: http.MethodPatch,
			target: "/api/stacks/acme/proj/stk/update/upd-test0001/checkpointdelta",
		},
		{
			name:   "event batch",
			method: http.MethodPost,
			target: "/api/stacks/acme/proj/stk/update/upd-test0001/events/batch",
		},
		{name: "import", method: http.MethodPost, target: "/api/stacks/acme/proj/stk/import"},
		// The TFE state version create, without credentials.
		{
			name:   "state version create",
			method: http.MethodPost,
			target: "/api/v2/workspaces/" + wsID + "/state-versions",
		},
		// The TFE upload routes, with a wrong capability.
		{name: "state upload", method: http.MethodPut, target: "/api/v2/state-uploads/" + svID + "/" + wrong},
		{name: "rendering upload", method: http.MethodPut, target: "/api/v2/json-state-uploads/" + svID + "/" + wrong},
		// The http backend write, without credentials.
		{name: "http backend write", method: http.MethodPost, target: "/tofu/state/acme/web"},
	}
}

// controlCases lists one authenticated route per adapter.
func controlCases(wsID string) []controlCase {
	return []controlCase{
		{
			name:   "pulumi import",
			method: http.MethodPost,
			target: "/api/stacks/acme/proj/stk/import",
			header: "token " + adminToken,
		},
		{
			name:   "tfe create",
			method: http.MethodPost,
			target: "/api/v2/workspaces/" + wsID + "/state-versions",
			header: "Bearer " + adminToken,
		},
		{
			name:   "http backend write",
			method: http.MethodPost,
			target: "/tofu/state/acme/web",
			header: "Bearer " + adminToken,
		},
	}
}
