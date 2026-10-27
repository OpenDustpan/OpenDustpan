package dustpan_test

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// stackIDOf reads the stable ID of the stack acme/web/name through the API.
func stackIDOf(t *testing.T, f fixture, name string) string {
	t.Helper()

	status, body := f.do(t, http.MethodGet, "/api/stacks/acme/web/"+name, adminToken, "")
	if status != http.StatusOK {
		t.Fatalf("read stack %s: status %d, body %s", name, status, body)
	}

	_, rest, found := strings.Cut(body, `"id":"`)
	if !found {
		t.Fatalf("stack %s: no ID in %s", name, body)
	}

	id, _, _ := strings.Cut(rest, `"`)

	return id
}

// blobAuditWorld is a server on a real store and a real blob store, with the
// stack acme/web/prod and one checkpoint of it, and the stack acme/web/other.
type blobAuditWorld struct {
	st      *sqlite.Store
	blobs   *fs.Store
	counted *keyCounter
	f       fixture
	dbPath  string
	ns      namespace.Namespace
}

// newBlobAuditWorld opens the stores and starts the server. The blob store the
// server sees counts its reads.
func newBlobAuditWorld(t *testing.T) blobAuditWorld {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "dustpan.db")

	st, err := sqlite.Open(t.Context(), dbPath)
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

	counted := &keyCounter{Store: blobs, mu: sync.Mutex{}, gets: map[string]int{}}

	srv, err := dustpan.NewServer(append(fullServerOptions(t, st, blobs, authn), dustpan.WithBlobStore(counted))...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	f := fixture{store: st, server: ts, srv: srv}
	f.createNamespace(t, "acme", installationName)

	savePulumiCheckpoint(t, f)

	status, body := f.do(t, http.MethodPost, "/api/stacks/acme/web", adminToken, `{"stackName":"other"}`)
	if status != http.StatusOK {
		t.Fatalf("create the other stack: status %d, body %s", status, body)
	}

	ns, err := st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	return blobAuditWorld{st: st, blobs: blobs, counted: counted, f: f, dbPath: dbPath, ns: ns}
}

// TestRefusedStackBlobKeyReachesTheServerAuditChain is the wiring test of the
// stack blob scope. The server runs on a real store and a real blob store. The
// checkpoint row of the stack acme/web/prod is changed to name the blob of
// another stack of the same namespace, as a damaged or hostile database
// would. An export of the stack must answer with an error, must not read the
// blob of the other stack, and must write one security entry to the audit log
// of the scope @server, with the channel of the request. A second request
// from the console writes an entry on the console channel.
func TestRefusedStackBlobKeyReachesTheServerAuditChain(t *testing.T) {
	t.Parallel()

	w := newBlobAuditWorld(t)

	victim := blob.Key("namespaces/" + w.ns.ID.String() + "/pulumi/" + stackIDOf(t, w.f, "other") + "/upd-victim/1")
	if putErr := w.blobs.Put(t.Context(), victim, strings.NewReader("victim")); putErr != nil {
		t.Fatalf("put the blob of the other stack: %v", putErr)
	}

	plantBlobKey(t, w.dbPath, victim)

	before := w.counted.count("pulumi")

	// An API request, and a request that declares the console channel.
	for _, channel := range []string{"", "console"} {
		status := doChannel(t, w.f, "/api/stacks/acme/web/prod/export", channel)
		if status == http.StatusOK {
			t.Fatalf("export with a foreign blob key answers %d, want an error", status)
		}
	}

	if got := w.counted.count("pulumi") - before; got != 0 {
		t.Fatalf("the exports read %d Pulumi blobs through the store, want 0", got)
	}

	checkSecurityEntries(t, w.st, w.ns.ID, []audit.Channel{audit.ChannelAPI, audit.ChannelConsole})
}

// plantBlobKey sets the blob key of every checkpoint row to key, with a second
// connection to the database, as a damaged or hostile database would be.
func plantBlobKey(t *testing.T, dbPath string, key blob.Key) {
	t.Helper()

	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	}()

	res, err := raw.ExecContext(t.Context(), `UPDATE checkpoints SET blob_key = ?`, key.String())
	if err != nil {
		t.Fatalf("plant the blob key: %v", err)
	}

	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		t.Fatalf("rows with the planted blob key = %d, %v; want at least one", n, err)
	}
}

// doChannel sends a GET to path as the administrator, declaring channel in the
// header that clients use for it when channel is not empty.
func doChannel(t *testing.T, f fixture, path, channel string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+path, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)

	if channel != "" {
		req.Header.Set("Dustpan-Channel", channel)
	}

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	return resp.StatusCode
}

// checkSecurityEntries reads the audit log of the scope @server and checks
// that it holds one security entry for each channel in want, in order, for
// the namespace ns, and that the chain of the scope is valid.
func checkSecurityEntries(t *testing.T, st *sqlite.Store, ns namespace.ID, want []audit.Channel) {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list the server audit log: %v", err)
	}

	var got []int

	for i := range listing.Entries {
		if listing.Entries[i].Action == "blob:out-of-scope" {
			got = append(got, i)
		}
	}

	if len(got) != len(want) {
		t.Fatalf("security entries = %d, want %d: %+v", len(got), len(want), listing.Entries)
	}

	// The listing is newest first.
	for n, i := range got {
		checkSecurityEntry(t, &listing.Entries[i], ns, want[len(want)-1-n])
	}

	res, checkErr := st.Check(t.Context(), audit.ServerScope)
	if checkErr != nil || !res.Valid {
		t.Fatalf("check the server audit log = %+v, %v; want a valid chain", res, checkErr)
	}
}

// checkSecurityEntry checks one entry of a refusal: the kind, the outcome, the
// object and the channel, and that the reason holds no blob key.
func checkSecurityEntry(t *testing.T, e *audit.Entry, ns namespace.ID, channel audit.Channel) {
	t.Helper()

	if e.Kind != audit.KindSecurity || e.Outcome != audit.OutcomeDenied || e.Object != ns.String() ||
		e.Channel != channel {
		t.Errorf("entry = kind %q outcome %q object %q channel %q; want security, denied, %s, %s",
			e.Kind, e.Outcome, e.Object, e.Channel, ns, channel)
	}

	if want := "stack blob get refused: key outside the unit directory"; e.Reason != want {
		t.Errorf("entry reason = %q, want %q", e.Reason, want)
	}

	if strings.Contains(e.Reason, "victim") || strings.Contains(e.Reason, "upd-") {
		t.Errorf("entry reason names a blob key: %q", e.Reason)
	}
}

// bareNamespaceStore is a namespace store of an embedder that keeps no audit
// entries of the scope @server: embedding the interface hides every other
// method of the store under it.
type bareNamespaceStore struct{ namespace.Store }

// TestNewServerNeedsServerAuditForStackBlobs checks the start requirement: a
// namespace store that cannot append audit entries of the scope @server makes
// NewServer answer ErrMissingOption, and the error names the option and the
// interface. The refusals of the stack blob scope need a place to go, so the
// server never starts without it.
func TestNewServerNeedsServerAuditForStackBlobs(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithNamespaceStore(bareNamespaceStore{st}))

	_, err := dustpan.NewServer(opts...)
	if !errors.Is(err, dustpan.ErrMissingOption) {
		t.Fatalf("NewServer with a store that appends no server entries: err = %v, want ErrMissingOption", err)
	}

	if !strings.Contains(err.Error(), "WithNamespaceStore") {
		t.Fatalf("err = %q, want it to name WithNamespaceStore", err)
	}
}
