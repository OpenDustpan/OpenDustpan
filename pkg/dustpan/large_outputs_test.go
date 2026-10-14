package dustpan_test

import (
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// largeOutputsFixture holds a server with a custom body budget and its blob
// store for orphan checks.
type largeOutputsFixture struct {
	base  fixture
	blobs *fs.Store
}

// newLargeOutputsFixture returns a server whose body budget holds n bytes.
func newLargeOutputsFixture(t *testing.T, n int64) largeOutputsFixture {
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
		dustpan.WithRegion(region),
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
		dustpan.WithMaxBodyBytesInFlight(n),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return largeOutputsFixture{base: fixture{store: st, server: ts, srv: srv}, blobs: blobs}
}

// newDefaultOutputsFixture returns a server with the default body budget.
func newDefaultOutputsFixture(t *testing.T) largeOutputsFixture {
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
		dustpan.WithRegion(region),
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
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return largeOutputsFixture{base: fixture{store: st, server: ts, srv: srv}, blobs: blobs}
}

// largeOutputsState returns a state whose outputs value holds about 5 MiB.
func largeOutputsState(serial int64) string {
	var b strings.Builder

	b.WriteString(`{"version":4,"terraform_version":"1.9.0","serial":`)
	b.WriteString(strconv.FormatInt(serial, 10))
	b.WriteString(`,"lineage":"l1","outputs":{"o":{"value":"`)
	b.WriteString(strings.Repeat("o", 5<<20))
	b.WriteString(`","type":"string"}},"resources":[],"check_results":null}`)

	return b.String()
}

// smallState returns a small state for the follow-up upload.
func smallState(serial int64) string {
	return `{"version":4,"serial":` + strconv.FormatInt(serial, 10) + `,"lineage":"l1","outputs":{},"resources":[]}`
}

// prepareLargeWorkspace creates a namespace, a locked workspace and a pending
// version for content.
func prepareLargeWorkspace(
	t *testing.T,
	f largeOutputsFixture,
	content string,
	serial int64,
) (workspace.Workspace, workspace.PendingState) {
	t.Helper()

	f.base.createNamespace(t, "acme", region)

	ctx := t.Context()

	name, parseErr := workspace.ParseName("web")
	if parseErr != nil {
		t.Fatalf("parse workspace name: %v", parseErr)
	}

	ws, createErr := f.base.srv.Workspaces().Create(ctx, workspace.Ref{Namespace: "acme", Name: name})
	if createErr != nil {
		t.Fatalf("create workspace: %v", createErr)
	}

	if _, lockErr := f.base.srv.Workspaces().Lock(ctx, ws.ID, "admin", "apply"); lockErr != nil {
		t.Fatalf("lock workspace: %v", lockErr)
	}

	sum := md5.Sum([]byte(content)) //nolint:gosec // The TFE protocol names state by MD5.

	pending, beginErr := f.base.srv.Workspaces().BeginState(ctx, ws.ID, "admin", workspace.NewState{
		Lineage: "l1",
		MD5:     hex.EncodeToString(sum[:]),
		Outputs: nil,
		Serial:  serial,
		Force:   false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if beginErr != nil {
		t.Fatalf("begin state: %v", beginErr)
	}

	return ws, pending
}

// putStateUpload PUTs content to the state upload URL and returns its status
// and Retry-After.
func putStateUpload(t *testing.T, f largeOutputsFixture, pending workspace.PendingState, content string) (int, string) {
	t.Helper()

	target := f.base.server.URL + "/api/v2/state-uploads/" + pending.Version.ID.String() + "/" + pending.Upload.Reveal()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, target, strings.NewReader(content))
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}

	req.ContentLength = -1

	resp, err := f.base.server.Client().Do(req)
	if err != nil {
		t.Fatalf("put upload: %v", err)
	}

	_, err = io.Copy(io.Discard, resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close upload body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read upload body: %v", err)
	}

	return resp.StatusCode, resp.Header.Get("Retry-After")
}

// blobTotal returns how many blobs the workspace holds.
func blobTotal(t *testing.T, f largeOutputsFixture, ws workspace.Workspace) int {
	t.Helper()

	listed, err := f.blobs.List(t.Context(), "tofu/"+ws.ID.String()+"/")
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}

	return len(listed)
}

// postHTTPBackend POSTs content to the http backend address.
func postHTTPBackend(t *testing.T, f largeOutputsFixture, lockID, content string) (int, string) {
	t.Helper()

	target := f.base.server.URL + "/tofu/state/acme/web?ID=" + lockID

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(content))
	if err != nil {
		t.Fatalf("new post request: %v", err)
	}

	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := f.base.server.Client().Do(req)
	if err != nil {
		t.Fatalf("post state: %v", err)
	}

	_, err = io.Copy(io.Discard, resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close post body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read post body: %v", err)
	}

	return resp.StatusCode, resp.Header.Get("Retry-After")
}

// prepareHTTPWorkspace creates a namespace and a locked workspace for the http
// backend flow.
func prepareHTTPWorkspace(t *testing.T, f largeOutputsFixture) (workspace.Workspace, string) {
	t.Helper()

	f.base.createNamespace(t, "acme", region)

	ctx := t.Context()

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	ws, err := f.base.srv.Workspaces().Create(ctx, workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	locked, err := f.base.srv.Workspaces().Lock(ctx, ws.ID, "admin", "apply")
	if err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	return locked, locked.Lock.ID
}

// TestLargeOutputsGrowReservation proves an upload with a large outputs
// document grows its reservation: with a 12 MiB budget it answers 503 with
// Retry-After, leaves no blob, and frees the budget, while the default budget
// serves it. The http backend POST behaves the same.
func TestLargeOutputsGrowReservation(t *testing.T) {
	t.Parallel()

	t.Run("tfe refuses with a small gate and serves with the default", func(t *testing.T) {
		t.Parallel()
		checkLargeTFE(t)
	})

	t.Run("http backend refuses with a small gate and serves with the default", func(t *testing.T) {
		t.Parallel()
		checkLargeHTTP(t)
	})
}

// checkLargeTFE runs the TFE part of the large outputs reservation test.
func checkLargeTFE(t *testing.T) {
	t.Helper()

	content := largeOutputsState(1)

	small := newLargeOutputsFixture(t, 12<<20)
	ws, pending := prepareLargeWorkspace(t, small, content, 1)

	checkTFERefused(t, small, ws, pending, content)
	checkTFEFollowUp(t, small, ws)
	checkTFEDefault(t, content)
}

// checkTFERefused proves the large upload meets 503 with no blob left.
func checkTFERefused(
	t *testing.T,
	small largeOutputsFixture,
	ws workspace.Workspace,
	pending workspace.PendingState,
	content string,
) {
	t.Helper()

	status, retry := putStateUpload(t, small, pending, content)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("upload with 12 MiB gate answers %d, want 503", status)
	}

	if retry != "1" {
		t.Fatalf("Retry-After = %q, want %q", retry, "1")
	}

	if got := blobTotal(t, small, ws); got != 0 {
		t.Fatalf("blobs = %d after a refused upload, want none", got)
	}
}

// checkTFEFollowUp proves a small upload succeeds after the refusal.
func checkTFEFollowUp(t *testing.T, small largeOutputsFixture, ws workspace.Workspace) {
	t.Helper()

	follow := smallState(1)
	followSum := md5.Sum([]byte(follow)) //nolint:gosec // The TFE protocol names state by MD5.

	followPending, beginErr := small.base.srv.Workspaces().BeginState(t.Context(), ws.ID, "admin", workspace.NewState{
		Lineage: "l1",
		MD5:     hex.EncodeToString(followSum[:]),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
	if beginErr != nil {
		t.Fatalf("begin follow-up state: %v", beginErr)
	}

	status, _ := putStateUpload(t, small, followPending, follow)
	if status != http.StatusOK {
		t.Fatalf("follow-up upload answers %d, want 200", status)
	}
}

// checkTFEDefault proves the large upload succeeds with the default budget.
func checkTFEDefault(t *testing.T, content string) {
	t.Helper()

	def := newDefaultOutputsFixture(t)
	_, defPending := prepareLargeWorkspace(t, def, content, 1)

	status, _ := putStateUpload(t, def, defPending, content)
	if status != http.StatusOK {
		t.Fatalf("upload with the default gate answers %d, want 200", status)
	}
}

// checkLargeHTTP runs the http backend part of the reservation test.
func checkLargeHTTP(t *testing.T) {
	t.Helper()

	content := largeOutputsState(1)

	small := newLargeOutputsFixture(t, 12<<20)
	ws, lockID := prepareHTTPWorkspace(t, small)

	status, retry := postHTTPBackend(t, small, lockID, content)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("POST with 12 MiB gate answers %d, want 503", status)
	}

	if retry != "1" {
		t.Fatalf("Retry-After = %q, want %q", retry, "1")
	}

	if got := blobTotal(t, small, ws); got != 0 {
		t.Fatalf("blobs = %d after a refused POST, want none", got)
	}

	follow := smallState(1)
	status, _ = postHTTPBackend(t, small, lockID, follow)
	if status != http.StatusOK {
		t.Fatalf("follow-up POST answers %d, want 200", status)
	}

	def := newDefaultOutputsFixture(t)
	_, defLock := prepareHTTPWorkspace(t, def)

	status, _ = postHTTPBackend(t, def, defLock, content)
	if status != http.StatusOK {
		t.Fatalf("POST with the default gate answers %d, want 200", status)
	}
}
