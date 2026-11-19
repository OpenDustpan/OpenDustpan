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

	"opendustpan.org/opendustpan/pkg/blob/blobtest"
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
		dustpan.WithMaxBodyBytesInFlight(n),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
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
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
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

	f.base.createNamespace(t, "acme", installationName)

	ctx := t.Context()

	name, parseErr := workspace.ParseName("web")
	if parseErr != nil {
		t.Fatalf("parse workspace name: %v", parseErr)
	}

	ws, createErr := f.base.srv.Workspaces().Create(
		ctx, workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, f.base.store, "acme"),
	)
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
// and body.
func putStateUpload(
	t *testing.T, f largeOutputsFixture, pending workspace.PendingState, content string,
) (int, string) {
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

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close upload body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read upload body: %v", err)
	}

	return resp.StatusCode, string(data)
}

// blobTotal returns how many blobs the workspace holds.
func blobTotal(t *testing.T, f largeOutputsFixture, ws workspace.Workspace) int {
	t.Helper()

	listed, err := blobtest.ListAll(t.Context(), f.blobs, "namespaces/")
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}

	n := 0

	for _, obj := range listed {
		if parsed, parseErr := workspace.ParseStateBlobKey(obj.Key); parseErr == nil && parsed.Workspace == ws.ID {
			n++
		}
	}

	return n
}

// postHTTPBackend POSTs content to the http backend address and returns its
// status, Retry-After and body.
//
//nolint:revive // Three unnamed results keep the helper short: status, Retry-After, body.
func postHTTPBackend(t *testing.T, f largeOutputsFixture, lockID, content string) (int, string, string) {
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

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close post body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read post body: %v", err)
	}

	return resp.StatusCode, resp.Header.Get("Retry-After"), string(data)
}

// checkTransientRetryAfter proves the Retry-After of a transient budget
// refusal is 1, 2 or 3 seconds.
func checkTransientRetryAfter(t *testing.T, retry string) {
	t.Helper()

	switch retry {
	case "1", "2", "3":
	default:
		t.Fatalf("Retry-After = %q, want 1, 2 or 3", retry)
	}
}

// checkTooSmallBody proves the body names the budget flag.
func checkTooSmallBody(t *testing.T, body string) {
	t.Helper()

	if !strings.Contains(body, "--max-body-bytes-in-flight") {
		t.Fatalf("body %q lacks the flag name", body)
	}
}

// prepareHTTPWorkspace creates a namespace and a locked workspace for the http
// backend flow.
func prepareHTTPWorkspace(t *testing.T, f largeOutputsFixture) (workspace.Workspace, string) {
	t.Helper()

	f.base.createNamespace(t, "acme", installationName)

	ctx := t.Context()

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	ws, err := f.base.srv.Workspaces().Create(
		ctx, workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, f.base.store, "acme"),
	)
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
// document grows its reservation: with a 12 MiB budget it answers 413, leaves
// no blob, and frees the budget, while the default budget serves it. The http
// backend POST behaves the same.
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

// checkTFERefused proves the large upload meets 413 with no blob left.
func checkTFERefused(
	t *testing.T,
	small largeOutputsFixture,
	ws workspace.Workspace,
	pending workspace.PendingState,
	content string,
) {
	t.Helper()

	status, body := putStateUpload(t, small, pending, content)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload with 12 MiB gate answers %d, want 413", status)
	}

	checkTooSmallBody(t, body)

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

	status, _, body := postHTTPBackend(t, small, lockID, content)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST with 12 MiB gate answers %d, want 413", status)
	}

	checkTooSmallBody(t, body)

	if got := blobTotal(t, small, ws); got != 0 {
		t.Fatalf("blobs = %d after a refused POST, want none", got)
	}

	follow := smallState(1)
	status, _, _ = postHTTPBackend(t, small, lockID, follow)
	if status != http.StatusOK {
		t.Fatalf("follow-up POST answers %d, want 200", status)
	}

	def := newDefaultOutputsFixture(t)
	_, defLock := prepareHTTPWorkspace(t, def)

	status, _, _ = postHTTPBackend(t, def, defLock, content)
	if status != http.StatusOK {
		t.Fatalf("POST with the default gate answers %d, want 200", status)
	}
}

// TestLargeOutputsBusyIsTransient proves a large upload that fits alone
// answers 503 with jitter when the budget has no room, and 200 after the
// room frees. A 40 MiB gate holds one open POST (8 MiB) plus the 5 MiB
// outputs upload (about 34 MiB alone).
func TestLargeOutputsBusyIsTransient(t *testing.T) {
	t.Parallel()

	for attempt := range 5 {
		content := largeOutputsState(int64(attempt + 1))
		if tryTransientUpload(t, content) {
			return
		}
	}

	t.Fatal("the large upload never met the full gate in 5 attempts")
}

// startHolder starts one http backend POST with an open body under lockID and
// reports when it ends. The POST holds the fixed cost while its body stays
// open.
func startHolder(t *testing.T, f largeOutputsFixture, lockID string, reader *io.PipeReader) chan struct{} {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			f.base.server.URL+"/tofu/state/acme/web?ID="+lockID, reader)
		if err != nil {
			return
		}

		req.ContentLength = -1
		req.Header.Set("Authorization", "Bearer "+adminToken)

		resp, err := f.base.server.Client().Do(req)
		if err != nil {
			return
		}

		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Errorf("drain write response: %v", err)
		}

		if err := resp.Body.Close(); err != nil {
			t.Errorf("close write response: %v", err)
		}
	}()

	return done
}

// waitHolder waits until the open POST likely holds the gate.
func waitHolder(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
		t.Fatalf("context done waiting for the write: %v", t.Context().Err())
	case <-timer.C:
	}
}

// endHolder ends the open POST and waits for it.
func endHolder(t *testing.T, writer *io.PipeWriter, done chan struct{}) {
	t.Helper()

	if err := writer.Close(); err != nil {
		t.Fatalf("close write body: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the open POST did not finish after the body closed")
	}
}

// tryTransientUpload runs one transient attempt with a fresh 40 MiB server.
// It holds one POST open, POSTs content, and proves 503 with jitter while
// the holder runs and 200 after it ends. It reports whether the POST met the
// full gate.
func tryTransientUpload(t *testing.T, content string) bool {
	t.Helper()

	f := newLargeOutputsFixture(t, 40<<20)
	_, lockID := prepareHTTPWorkspace(t, f)

	reader, writer := io.Pipe()
	done := startHolder(t, f, lockID, reader)

	waitHolder(t)

	status, retry, _ := postHTTPBackend(t, f, lockID, content)
	if status != http.StatusServiceUnavailable {
		endHolder(t, writer, done)

		return false
	}

	checkTransientRetryAfter(t, retry)
	endHolder(t, writer, done)

	status, _, body := postHTTPBackend(t, f, lockID, content)
	if status != http.StatusOK {
		t.Fatalf("POST after the write answers %d, want 200: %s", status, body)
	}

	return true
}
