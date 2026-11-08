package dustpan_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/blob/blobfault"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func TestNewServerRefusesNegativeBlobCache(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithBlobCache(-1))

	if _, err := dustpan.NewServer(opts...); !errors.Is(err, dustpan.ErrInvalidOption) {
		t.Fatalf("NewServer with blob cache -1: err = %v, want ErrInvalidOption", err)
	}
}

// TestBlobCacheServesRepeatReads reads one OpenTofu state twice. With the
// blob cache, the second read does not reach the blob store.
func TestBlobCacheServesRepeatReads(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)

	counted, err := blobfault.New(blobs)
	if err != nil {
		t.Fatalf("blobfault: %v", err)
	}

	opts := append(fullServerOptions(t, st, blobs, authn),
		dustpan.WithBlobStore(counted),
		dustpan.WithBlobCache(1<<20),
	)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	err = st.CreateNamespace(t.Context(), namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                "acme",
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	svc := srv.Workspaces()
	if svc == nil {
		t.Fatal("server has no workspace service")
	}

	const content = `{"version":4,"serial":1,"lineage":"l1"}`

	w := createLockedWorkspace(t, svc, st)
	finalized := uploadWorkspaceState(t, svc, w.ID, content)

	checkWorkspaceContent(t, svc, w.ID, finalized.ID, content)

	first := counted.Calls("Get")

	checkWorkspaceContent(t, svc, w.ID, finalized.ID, content)

	if n := counted.Calls("Get"); n != first {
		t.Fatalf("blob store Get calls after a second read = %d, want %d", n, first)
	}
}

// keyCounter counts the Get calls on a blob store by key family: the first key
// segment, or the segment after the namespace ID for a key of a namespace.
type keyCounter struct {
	blob.Store

	mu   sync.Mutex
	gets map[string]int
}

func (c *keyCounter) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	parts := strings.Split(key.String(), "/")
	family := parts[0]

	if family == "namespaces" && len(parts) > 2 {
		family = parts[2]
	}

	c.mu.Lock()
	c.gets[family]++
	c.mu.Unlock()

	return c.Store.Get(ctx, key)
}

// count returns the Get calls on the keys of family.
func (c *keyCounter) count(family string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.gets[family]
}

// TestBlobCacheLeavesOtherReadsUncached proves the wiring of the blob cache:
// with WithBlobCache, Pulumi checkpoint reads and backups still reach the
// blob store. A backup of a cached OpenTofu state reads it twice from the
// store: once to copy it and once for the copy check.
func TestBlobCacheLeavesOtherReadsUncached(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	counted := &keyCounter{Store: blobs, mu: sync.Mutex{}, gets: map[string]int{}}

	// A server claims its installation kind at start, and the backup records
	// it.
	if _, claimErr := st.ClaimKind(t.Context(), installation.KindProduction); claimErr != nil {
		t.Fatalf("claim the installation kind: %v", claimErr)
	}

	target, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	srv, err := dustpan.NewServer(append(fullServerOptions(t, st, blobs, authn),
		dustpan.WithBlobStore(counted),
		dustpan.WithBlobCache(1<<20),
		dustpan.WithBackupTarget(target, "dir:/var/backups"),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	f := fixture{store: st, server: ts, srv: srv}
	f.createNamespace(t, "acme", installationName)

	savePulumiCheckpoint(t, f)

	for i := range 2 {
		before := counted.count("pulumi")

		status, body := f.do(t, http.MethodGet, "/api/stacks/acme/web/prod/export", adminToken, "")
		if status != http.StatusOK {
			t.Fatalf("export: status %d, body %s", status, body)
		}

		if counted.count("pulumi") == before {
			t.Fatalf("export %d read no Pulumi checkpoint from the blob store, want a read through", i+1)
		}
	}

	const content = `{"version":4,"serial":1,"lineage":"l1","outputs":{}}`

	w := createLockedWorkspace(t, srv.Workspaces(), st)
	finalized := uploadWorkspaceState(t, srv.Workspaces(), w.ID, content)
	checkWorkspaceContent(t, srv.Workspaces(), w.ID, finalized.ID, content)

	before := counted.count("tofu")

	id, err := backup.ParseID("bkp-cache0000000000001")
	if err != nil {
		t.Fatalf("parse backup ID: %v", err)
	}

	if _, err := srv.Backups().Run(t.Context(), id); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	if got := counted.count("tofu") - before; got != 2 {
		t.Fatalf("backup read the cached state %d times from the blob store, want 2: the copy and the copy check", got)
	}
}

// savePulumiCheckpoint creates the stack acme/web/prod and saves one
// checkpoint in a finished update through the API.
func savePulumiCheckpoint(t *testing.T, f fixture) {
	t.Helper()

	status, body := f.do(t, http.MethodPost, "/api/stacks/acme/web", adminToken, `{"stackName":"prod"}`)
	if status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", status, body)
	}

	var created struct {
		UpdateID string `json:"updateID"`
	}

	status, body = f.do(t, http.MethodPost, "/api/stacks/acme/web/prod/update", adminToken, `{}`)
	if status != http.StatusOK || json.Unmarshal([]byte(body), &created) != nil || created.UpdateID == "" {
		t.Fatalf("create update: status %d, body %s", status, body)
	}

	var started struct {
		Token string `json:"token"`
	}

	prefix := "/api/stacks/acme/web/prod/update/" + created.UpdateID

	status, body = f.do(t, http.MethodPost, prefix, adminToken, `{}`)
	if status != http.StatusOK || json.Unmarshal([]byte(body), &started) != nil || started.Token == "" {
		t.Fatalf("start update: status %d, body %s", status, body)
	}

	lease := "update-token " + started.Token

	status, body = doWithAuth(t, f, authRequest{
		method: http.MethodPatch, path: prefix + "/checkpoint", auth: lease,
		body: `{"version":3,"deployment":{"manifest":{},"resources":[]}}`,
	})
	if status != http.StatusOK {
		t.Fatalf("save checkpoint: status %d, body %s", status, body)
	}

	status, body = doWithAuth(t, f, authRequest{
		method: http.MethodPost, path: prefix + "/complete", auth: lease, body: `{"status":"succeeded"}`,
	})
	if status != http.StatusOK {
		t.Fatalf("complete update: status %d, body %s", status, body)
	}
}

// authRequest is one request with its own Authorization header value.
type authRequest struct {
	method string
	path   string
	auth   string
	body   string
}

// doWithAuth sends r and returns the status and the body.
func doWithAuth(t *testing.T, f fixture, r authRequest) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), r.method, f.server.URL+r.path, strings.NewReader(r.body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", r.auth)

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", r.method, r.path, err)
	}

	data, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}

	return resp.StatusCode, string(data)
}

// TestNewServerChecksTheAuditChainInterval proves that NewServer refuses an
// audit chain check interval of zero or less and accepts a positive one.
func TestNewServerChecksTheAuditChainInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		every   time.Duration
		wantErr bool
	}{
		{name: "zero", every: 0, wantErr: true},
		{name: "negative", every: -time.Second, wantErr: true},
		{name: "positive", every: time.Minute, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithAuditChainCheckInterval(tt.every))

			_, err := dustpan.NewServer(opts...)
			if tt.wantErr != errors.Is(err, dustpan.ErrInvalidOption) || (!tt.wantErr && err != nil) {
				t.Fatalf("NewServer with interval %s: err = %v, want invalid option = %t", tt.every, err, tt.wantErr)
			}
		})
	}
}
