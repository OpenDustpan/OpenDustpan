package dustpan_test

import (
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dustpan "opendustpan.org/opendustpan"
	"opendustpan.org/opendustpan/blob/fs"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/sqlite"
	"opendustpan.org/opendustpan/workspace"
)

const (
	adminToken = "test-token-0123456789abcdef0123456789"
	region     = "eu-west"
	origin     = "https://dustpan.acme.internal"
)

// fixture is a running server backed by a fresh SQLite database.
type fixture struct {
	store  *sqlite.Store
	server *httptest.Server
	srv    *dustpan.Server
}

func newFixture(t *testing.T) fixture {
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

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithAllowedOrigins(origin),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return fixture{store: st, server: ts, srv: srv}
}

func (f fixture) createNamespace(t *testing.T, name, home string) {
	t.Helper()

	err := f.store.CreateNamespace(t.Context(), namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            namespace.Name(name),
		HomeRegion:      namespace.Region(home),
		FailoverVersion: 1,
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}
}

func (f fixture) do(t *testing.T, method, path, token, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, string(data)
}

func TestStackLifecycle(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", region)

	status, body := f.do(t, http.MethodPost, "/api/stacks/acme/web", adminToken, `{"stackName":"prod"}`)
	if status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", status, body)
	}

	status, body = f.do(t, http.MethodGet, "/api/stacks/acme/web/prod", adminToken, "")
	if status != http.StatusOK {
		t.Fatalf("get stack: status %d, body %s", status, body)
	}

	var got struct {
		OrgName     string `json:"orgName"`
		ProjectName string `json:"projectName"`
		StackName   string `json:"stackName"`
		ID          string `json:"id"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode stack: %v", err)
	}

	if got.OrgName != "acme" || got.ProjectName != "web" || got.StackName != "prod" {
		t.Fatalf("get stack = %+v", got)
	}

	if !strings.HasPrefix(got.ID, "stk-") {
		t.Fatalf("get stack ID = %q, want stk- prefix", got.ID)
	}

	status, body = f.do(t, http.MethodGet, "/api/user/stacks?organization=acme", adminToken, "")
	if status != http.StatusOK || !strings.Contains(body, `"stackName":"prod"`) {
		t.Fatalf("list stacks: status %d, body %s", status, body)
	}
}

// TestInventoryRouting proves dustpan.go mounts the inventory API at
// /api/inventory/, a pattern more specific than the Pulumi /api/ pattern: the
// unit list serves the inventory envelope, while the Pulumi CLI routes keep
// their shape.
func TestInventoryRouting(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", region)

	status, body := f.do(t, http.MethodGet, "/api/inventory/units?organization=acme", adminToken, "")
	if status != http.StatusOK || !strings.Contains(body, `"units":[]`) {
		t.Fatalf("list units: status %d, body %s", status, body)
	}

	status, body = f.do(t, http.MethodGet, "/api/inventory/units?organization=ghost", adminToken, "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown namespace: status %d, want %d (body %s)", status, http.StatusNotFound, body)
	}

	status, body = f.do(t, http.MethodGet, "/api/inventory/units", adminToken, "")
	if status != http.StatusBadRequest {
		t.Fatalf("missing organization: status %d, want %d (body %s)", status, http.StatusBadRequest, body)
	}

	status, body = f.do(t, http.MethodPost, "/api/stacks/acme/web", adminToken, `{"stackName":"prod"}`)
	if status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", status, body)
	}

	status, body = f.do(t, http.MethodGet, "/api/inventory/units?organization=acme", adminToken, "")
	if status != http.StatusOK ||
		!strings.Contains(body, `"tool":"Pulumi"`) ||
		!strings.Contains(body, `"reference":"pulumi:acme/web/prod"`) {
		t.Fatalf("list units after create: status %d, body %s", status, body)
	}

	status, body = f.do(t, http.MethodGet, "/api/user/stacks?organization=acme", adminToken, "")
	if status != http.StatusOK || !strings.Contains(body, `"stackName":"prod"`) {
		t.Fatalf("list stacks: status %d, body %s", status, body)
	}
}

// TestServerRouting proves dustpan.go mounts the installation API at
// /api/server/, a pattern more specific than the Pulumi /api/ pattern: an
// unknown /api/server/ path answers 404 as JSON from the installation
// adapter, while the Pulumi CLI routes keep their shape.
func TestServerRouting(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		status, header, body := f.doRaw(t, method, "/api/server/no-such-route", "")
		if status != http.StatusNotFound {
			t.Fatalf("unknown server path status %d, want %d (body %s)", status, http.StatusNotFound, body)
		}

		if got := header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("media type = %q, want the installation JSON error", got)
		}

		if !strings.Contains(body, `"code":404`) {
			t.Fatalf("unknown server path body %s, want a JSON error with code 404", body)
		}
	}

	status, _, body := f.doRaw(t, http.MethodGet, "/api/user", "token "+adminToken)
	if status != http.StatusOK || !strings.Contains(body, "admin") {
		t.Fatalf("user route: status %d, body %s", status, body)
	}
}

func TestWorkspaceLifecycle(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", region)

	svc := f.srv.Workspaces()
	if svc == nil {
		t.Fatal("server has no workspace service")
	}

	w := createLockedWorkspace(t, svc)
	finalized := uploadWorkspaceState(t, svc, w.ID, `{"version":4,"serial":1,"lineage":"l1"}`)
	checkWorkspaceContent(t, svc, w.ID, finalized.ID, `{"version":4,"serial":1,"lineage":"l1"}`)
}

// createLockedWorkspace creates a workspace in acme and locks it for apply.
func createLockedWorkspace(t *testing.T, svc *workspace.Service) workspace.Workspace {
	t.Helper()

	name, err := workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	w, err := svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	locked, err := svc.Lock(t.Context(), w.ID, "admin", "apply")
	if err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	return locked
}

// uploadWorkspaceState declares and uploads content as serial 1 of lineage l1.
func uploadWorkspaceState(
	t *testing.T,
	svc *workspace.Service,
	id workspace.ID,
	content string,
) workspace.StateVersion {
	t.Helper()

	//nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	sum := md5.Sum([]byte(content))

	pending, err := svc.BeginState(t.Context(), id, "admin", workspace.NewState{
		Lineage: "l1",
		MD5:     hex.EncodeToString(sum[:]),
		Outputs: nil,
		Serial:  1,
		Force:   false,
	})
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	finalized, err := svc.Upload(t.Context(), pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload state: %v", err)
	}

	return finalized
}

// checkWorkspaceContent proves the current state of id is wantID with content want.
func checkWorkspaceContent(
	t *testing.T,
	svc *workspace.Service,
	id workspace.ID,
	wantID workspace.StateVersionID,
	want string,
) {
	t.Helper()

	current, err := svc.Current(t.Context(), id)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	if current.ID != wantID {
		t.Fatalf("current state = %q, want %q", current.ID, wantID)
	}

	r, err := svc.Open(t.Context(), current)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}

	got, err := io.ReadAll(r)
	if closeErr := r.Close(); closeErr != nil {
		t.Fatalf("close state: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	if string(got) != want {
		t.Fatalf("state content = %q, want %q", got, want)
	}
}

func TestErrorStatuses(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", region)
	f.createNamespace(t, "faraway", "us-east")

	status, body := f.do(t, http.MethodPost, "/api/stacks/acme/web", adminToken, `{"stackName":"prod"}`)
	if status != http.StatusOK {
		t.Fatalf("seed stack: status %d, body %s", status, body)
	}

	cases := []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"no token", http.MethodGet, "/api/user", "", "", http.StatusUnauthorized},
		{"wrong token", http.MethodGet, "/api/user", "wrong", "", http.StatusUnauthorized},
		{"missing stack", http.MethodGet, "/api/stacks/acme/web/dev", adminToken, "", http.StatusNotFound},
		{
			"invalid name",
			http.MethodPost,
			"/api/stacks/acme/web",
			adminToken,
			`{"stackName":"../x"}`,
			http.StatusBadRequest,
		},
		{"duplicate", http.MethodPost, "/api/stacks/acme/web", adminToken, `{"stackName":"prod"}`, http.StatusConflict},
		{
			"missing namespace",
			http.MethodPost,
			"/api/stacks/ghost/web",
			adminToken,
			`{"stackName":"a"}`,
			http.StatusNotFound,
		},
		{
			"other region",
			http.MethodPost,
			"/api/stacks/faraway/web",
			adminToken,
			`{"stackName":"a"}`,
			http.StatusMisdirectedRequest,
		},
		{"bad json", http.MethodPost, "/api/stacks/acme/web", adminToken, `{`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if status, body := f.do(t, tc.method, tc.path, tc.token, tc.body); status != tc.want {
				t.Fatalf("status %d, want %d (body %s)", status, tc.want, body)
			}
		})
	}
}

// TestTFERouting proves /api/v2/ping reaches the TFE adapter, a Pulumi route
// reaches the Pulumi adapter, and a /api/v2/ path the TFE table does not hold
// answers 404 from the TFE adapter.
func TestTFERouting(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	t.Run("ping reaches the TFE adapter", func(t *testing.T) {
		t.Parallel()

		status, header, _ := f.doRaw(t, http.MethodGet, "/api/v2/ping", "")
		if status != http.StatusOK {
			t.Fatalf("ping status %d, want %d", status, http.StatusOK)
		}

		if got := header.Get("Tfp-Api-Version"); got != "2.5" {
			t.Fatalf("TFP-API-Version = %q, want %q", got, "2.5")
		}
	})

	t.Run("a Pulumi route reaches the Pulumi adapter", func(t *testing.T) {
		t.Parallel()

		status, _, body := f.doRaw(t, http.MethodGet, "/api/user", "token "+adminToken)
		if status != http.StatusOK || !strings.Contains(body, "admin") {
			t.Fatalf("user route: status %d, body %s", status, body)
		}
	})

	t.Run("an unknown v2 path answers 404 from the TFE adapter", func(t *testing.T) {
		t.Parallel()

		status, header, body := f.doRaw(t, http.MethodGet, "/api/v2/no-such-route", "")
		if status != http.StatusNotFound {
			t.Fatalf("unknown v2 path status %d, want %d", status, http.StatusNotFound)
		}

		if got := header.Get("Content-Type"); got != "application/vnd.api+json" {
			t.Fatalf("media type = %q, want the TFE JSON:API error", got)
		}

		if !strings.Contains(body, `"errors"`) {
			t.Fatalf("unknown v2 path body %s, want a JSON:API error", body)
		}
	})
}

// doRaw sends one request with auth as the whole Authorization header value
// (empty for none) and returns its status, headers and body.
func (f fixture) doRaw(t *testing.T, method, path, auth string) (int, http.Header, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
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

func TestCORS(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, tc := range []struct {
		name, origin, wantAllow string
	}{
		{"allowed origin", origin, origin},
		{"other origin", "https://evil.example", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			url := f.server.URL + "/api/user"

			req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, url, http.NoBody)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", http.MethodGet)

			resp, err := f.server.Client().Do(req)
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}

			if closeErr := resp.Body.Close(); closeErr != nil {
				t.Fatalf("close body: %v", closeErr)
			}

			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != tc.wantAllow {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantAllow)
			}
		})
	}
}

func TestNewServerRequiresPorts(t *testing.T) {
	t.Parallel()

	if _, err := dustpan.NewServer(dustpan.WithRegion(region)); err == nil {
		t.Fatal("NewServer without stores or authenticator succeeded")
	}

	st, blobs, authn := requireServerPorts(t)
	full := fullServerOptions(st, blobs, authn)

	cases := []struct {
		name string
		drop int
	}{
		{name: "namespace store", drop: 1},
		{name: "stack store", drop: 2},
		{name: "update store", drop: 3},
		{name: "checkpoint store", drop: 4},
		{name: "event store", drop: 5},
		{name: "secret key store", drop: 6},
		{name: "authenticator", drop: 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkMissingOption(t, full, tc.drop, tc.name)
		})
	}
}

func TestNewServerRequiresWorkspacePorts(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	full := fullServerOptions(st, blobs, authn)

	cases := []struct {
		name string
		drop int
	}{
		{name: "workspace store", drop: 8},
		{name: "state store", drop: 9},
		{name: "blob store", drop: 10},
		{name: "inventory reader", drop: 11},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]dustpan.Option(nil), full[:tc.drop]...)
			opts = append(opts, full[tc.drop+1:]...)

			_, err := dustpan.NewServer(opts...)
			if err == nil {
				t.Fatalf("NewServer without %s succeeded", tc.name)
			}

			if !errors.Is(err, dustpan.ErrMissingOption) {
				t.Fatalf("NewServer without %s: err = %v, want ErrMissingOption", tc.name, err)
			}
		})
	}
}

// requireServerPorts opens a SQLite store, a blob store and an authenticator.
func requireServerPorts(t *testing.T) (*sqlite.Store, *fs.Store, *identity.StaticToken) {
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

	return st, blobs, authn
}

// fullServerOptions returns every required NewServer option in a fixed order.
func fullServerOptions(st *sqlite.Store, blobs *fs.Store, authn *identity.StaticToken) []dustpan.Option {
	return []dustpan.Option{
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
	}
}

// checkMissingOption proves NewServer without full[drop] fails.
func checkMissingOption(t *testing.T, full []dustpan.Option, drop int, name string) {
	t.Helper()

	opts := append([]dustpan.Option(nil), full[:drop]...)
	opts = append(opts, full[drop+1:]...)

	_, err := dustpan.NewServer(opts...)
	if err == nil {
		t.Fatalf("NewServer without %s succeeded", name)
	}

	if !errors.Is(err, dustpan.ErrMissingOption) &&
		!errors.Is(err, namespace.ErrMissingDependency) &&
		!errors.Is(err, stack.ErrMissingDependency) {
		t.Fatalf("NewServer without %s: err = %v, want ErrMissingOption or ErrMissingDependency", name, err)
	}
}

func TestConsoleDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>console</p>"), 0o600); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

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
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithConsoleDir(dir),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	f := fixture{store: st, server: ts, srv: srv}

	cases := []struct {
		name, method, path, token, wantBody string
		want                                int
	}{
		{"index", http.MethodGet, "/", "", "console", http.StatusOK},
		{"client route falls back to index", http.MethodGet, "/stacks/acme", "", "console", http.StatusOK},
		{"missing asset is 404", http.MethodGet, "/missing.js", "", "", http.StatusNotFound},
		{"writes are rejected", http.MethodPost, "/", "", "", http.StatusMethodNotAllowed},
		{"api still routed", http.MethodGet, "/api/user", adminToken, "admin", http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := f.do(t, tc.method, tc.path, tc.token, "")
			if status != tc.want || !strings.Contains(body, tc.wantBody) {
				t.Fatalf("status %d body %q, want %d containing %q", status, body, tc.want, tc.wantBody)
			}
		})
	}
}
