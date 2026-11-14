package dustpan_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/bodygate"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// newGateFixture returns a server whose body budget holds one streaming
// OpenTofu upload plus 1 MiB, so a second large body does not fit while the
// first runs.
func newGateFixture(t *testing.T) fixture {
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
		dustpan.WithMaxBodyBytesInFlight(bodygate.StreamCost+1<<20),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return fixture{store: st, server: ts, srv: srv}
}

// gateCreateBody returns a valid state version create body padded with
// trailing spaces to a declared Content-Length of size bytes.
func gateCreateBody(size int) string {
	body := `{"data":{"type":"state-versions","attributes":{` +
		`"serial":1,"lineage":"l1","md5":"d41d8cd98f00b204e9800998ecf8427e"}}}`

	return body + strings.Repeat(" ", size-len(body))
}

// postRaw sends one request with a Bearer token and a body reader, and
// returns its status, headers and body.
func postRaw(
	t *testing.T, client *http.Client, url, token, body string,
) (int, http.Header, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/vnd.api+json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, resp.Header, string(data)
}

// TestSharedBodyGateAcrossAdapters proves one gate serves both OpenTofu
// adapters: while an http backend write with an unknown length holds
// StreamCost, a TFE state version create with a declared length of 2 MiB
// answers 503 with Retry-After. After the write ends, the create runs past
// the gate again.
func TestSharedBodyGateAcrossAdapters(t *testing.T) {
	t.Parallel()

	f := newGateFixture(t)
	f.createNamespace(t, "acme", installationName)

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	ws, err := f.srv.Workspaces().Create(
		t.Context(), workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, f.store, "acme"),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	locked, err := f.srv.Workspaces().Lock(t.Context(), ws.ID, "admin", "apply")
	if err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	create := gateCreateBody(2 << 20)
	target := "/api/v2/workspaces/" + ws.ID.String() + "/state-versions"

	// The write starts first and blocks reading its open body while it holds
	// StreamCost. The create runs once the write holds the gate: a 503
	// proves the shared budget refused it, while any other answer means the
	// write was not there yet and the attempt repeats with a fresh write.
	shared := false

	for range 5 {
		if trySharedGate(t, f, locked.Lock.ID, target, create) {
			shared = true

			break
		}
	}

	if !shared {
		t.Fatal("the create never met the full gate in 5 attempts")
	}
}

// sharedAttempt carries one sharing attempt's server, open write body,
// write result, and create target and body.
type sharedAttempt struct {
	f      fixture
	writer *io.PipeWriter
	done   chan struct{}
	target string
	create string
}

// trySharedGate runs one sharing attempt: it starts an http backend write
// with an open body under the lock, waits until the write likely holds the
// gate, then sends the TFE create. It reports whether the create met the full
// gate with 503. Either way the write ends before it returns; after a 503 the
// create runs again past the freed gate and must answer 201, not 503.
func trySharedGate(t *testing.T, f fixture, lockID, target, create string) bool {
	t.Helper()

	reader, writer := io.Pipe()

	writeDone := make(chan struct{})

	go func() {
		defer close(writeDone)

		req, err := http.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			f.server.URL+"/tofu/state/acme/web?ID="+lockID,
			reader,
		)
		if err != nil {
			return
		}

		req.ContentLength = -1
		req.Header.Set("Authorization", "Bearer "+adminToken)

		resp, err := f.server.Client().Do(req)
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

	// The write likely holds the gate once the timer ends: the request runs
	// on the loopback interface with no other work queued.
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-t.Context().Done():
		t.Fatalf("context done waiting for the write: %v", t.Context().Err())
	case <-timer.C:
	}

	attempt := sharedAttempt{f: f, writer: writer, done: writeDone, target: target, create: create}

	status, header, _ := postRaw(t, f.server.Client(), f.server.URL+target, adminToken, create)

	if status != http.StatusServiceUnavailable {
		finishWrite(t, writer, writeDone)

		return false
	}

	return proveSharedGate(t, attempt, header)
}

// proveSharedGate checks the Retry-After of the refused create, ends the
// running write, and proves the create runs past the freed gate with 201.
func proveSharedGate(t *testing.T, a sharedAttempt, header http.Header) bool {
	t.Helper()

	if got := header.Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want %q", got, "1")
	}

	finishWrite(t, a.writer, a.done)

	status, _, body := postRaw(t, a.f.server.Client(), a.f.server.URL+a.target, adminToken, a.create)
	if status == http.StatusServiceUnavailable {
		t.Fatalf("create after the write answers %d, want no 503: %s", status, body)
	}

	if status != http.StatusCreated {
		t.Fatalf("create after the write answers %d, want %d: %s", status, http.StatusCreated, body)
	}

	return true
}

// finishWrite ends the running http backend write and waits for it.
func finishWrite(t *testing.T, writer *io.PipeWriter, done chan struct{}) {
	t.Helper()

	if err := writer.Close(); err != nil {
		t.Fatalf("close write body: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the http backend write did not finish after the body closed")
	}
}
