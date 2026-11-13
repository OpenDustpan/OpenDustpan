package dustpan_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

const (
	adminToken       = "test-token-0123456789abcdef0123456789"
	installationName = "eu-west"
	origin           = "https://dustpan.acme.internal"
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

	// The fixture server seals OpenTofu state, so it holds a
	// key-encryption key like a server with a key set does.
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
		dustpan.WithAllowedOrigins(origin),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return fixture{store: st, server: ts, srv: srv}
}

func (f fixture) createNamespace(t *testing.T, name, home string) {
	t.Helper()

	err := f.store.CreateNamespace(t.Context(), namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                namespace.Name(name),
		PrimaryInstallation: namespace.InstallationName(home),
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	seedCatchAllPlacement(t, f.store, namespace.Name(name))
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
	f.createNamespace(t, "acme", installationName)

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
// /api/dustpan/v1/, a pattern more specific than the Pulumi /api/ pattern: the
// unit list serves the inventory envelope, while the Pulumi CLI routes keep
// their shape.
func TestInventoryRouting(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", installationName)

	status, body := f.do(t, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", adminToken, "")
	if status != http.StatusOK || !strings.Contains(body, `"units":[]`) {
		t.Fatalf("list units: status %d, body %s", status, body)
	}

	status, body = f.do(t, http.MethodGet, "/api/dustpan/v1/namespaces/ghost/units", adminToken, "")
	if status != http.StatusNotFound {
		t.Fatalf("unknown namespace: status %d, want %d (body %s)", status, http.StatusNotFound, body)
	}

	status, body = f.do(t, http.MethodGet, "/api/dustpan/v1/namespaces/Bad%20Name/units", adminToken, "")
	if status != http.StatusBadRequest {
		t.Fatalf("bad namespace name: status %d, want %d (body %s)", status, http.StatusBadRequest, body)
	}

	status, body = f.do(t, http.MethodPost, "/api/stacks/acme/web", adminToken, `{"stackName":"prod"}`)
	if status != http.StatusOK {
		t.Fatalf("create stack: status %d, body %s", status, body)
	}

	status, body = f.do(t, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", adminToken, "")
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
	f.createNamespace(t, "acme", installationName)

	svc := f.srv.Workspaces()
	if svc == nil {
		t.Fatal("server has no workspace service")
	}

	w := createLockedWorkspace(t, svc, f.store)
	finalized := uploadWorkspaceState(t, svc, w.ID, `{"version":4,"serial":1,"lineage":"l1"}`)
	checkWorkspaceContent(t, svc, w.ID, finalized.ID, `{"version":4,"serial":1,"lineage":"l1"}`)
}

// createLockedWorkspace creates a workspace in acme and locks it for apply.
func createLockedWorkspace(t *testing.T, svc *workspace.Service, store any) workspace.Workspace {
	t.Helper()

	name, err := workspace.ParseName("e2e-workspace")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	w, err := svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, store, "acme"))
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
	}, workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil})
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
	f.createNamespace(t, "acme", installationName)
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
			"other installation",
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

type corsRow struct {
	name, method, origin, path string
	wantAllow                  string
}

const corsOtherOrigin = "https://evil.example"

func corsRows() []corsRow {
	paths := []string{
		"/api/user", "/api/v2/ping", "/api/inventory/units", "/api/server/session",
		"/api/audit/events", "/api/context/objects",
	}
	rows := make([]corsRow, 0, 6*len(paths)+2)

	for _, p := range paths {
		rows = append(rows,
			corsRow{"preflight allowed origin " + p, http.MethodOptions, origin, p, origin},
			corsRow{"preflight other origin " + p, http.MethodOptions, corsOtherOrigin, p, ""},
			corsRow{"preflight no origin " + p, http.MethodOptions, "", p, ""},
			corsRow{"request allowed origin " + p, http.MethodGet, origin, p, origin},
			corsRow{"request other origin " + p, http.MethodGet, corsOtherOrigin, p, ""},
			corsRow{"request no origin " + p, http.MethodGet, "", p, ""},
		)
	}

	return append(rows,
		corsRow{"tofu state preflight", http.MethodOptions, origin, "/tofu/state/acme/net", ""},
		corsRow{"tofu state request", http.MethodGet, origin, "/tofu/state/acme/net", ""},
	)
}

func TestCORS(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, tc := range corsRows() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, header := doCORSRequest(t, f.server, tc)

			checkCORSHeaders(t, header, tc)
			checkPreflightStatus(t, status, tc)
		})
	}
}

func doCORSRequest(t *testing.T, ts *httptest.Server, tc corsRow) (int, http.Header) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), tc.method, ts.URL+tc.path, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if tc.origin != "" {
		req.Header.Set("Origin", tc.origin)
	}

	if tc.method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	return resp.StatusCode, resp.Header
}

func checkPreflightStatus(t *testing.T, status int, tc corsRow) {
	t.Helper()

	if tc.method != http.MethodOptions {
		return
	}

	if tc.wantAllow != "" && status != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", status)
	}

	if tc.origin == corsOtherOrigin && status == http.StatusNoContent {
		t.Fatal("preflight from another origin answered 204")
	}
}

func checkCORSHeaders(t *testing.T, h http.Header, tc corsRow) {
	t.Helper()

	method, wantAllow := tc.method, tc.wantAllow

	if got := h.Get("Access-Control-Allow-Origin"); got != wantAllow {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, wantAllow)
	}

	if !slices.Contains(h.Values("Vary"), "Origin") && !strings.HasPrefix(tc.path, "/tofu/") {
		t.Fatalf("Vary = %v, want Origin", h.Values("Vary"))
	}

	if wantAllow == "" {
		for _, name := range []string{
			"Access-Control-Allow-Credentials", "Access-Control-Allow-Headers", "Access-Control-Allow-Methods",
			"Access-Control-Max-Age", "Access-Control-Expose-Headers",
		} {
			if h.Get(name) != "" {
				t.Fatalf("%s set without an allowed origin: %v", name, h)
			}
		}

		return
	}

	if got := h.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}

	if method == http.MethodOptions {
		checkPreflightHeaders(t, h)

		return
	}

	if got := h.Get("Access-Control-Expose-Headers"); got != "Retry-After, ETag, Idempotent-Replayed" {
		t.Fatalf("Access-Control-Expose-Headers = %q, want Retry-After, ETag, Idempotent-Replayed", got)
	}
}

func checkPreflightHeaders(t *testing.T, h http.Header) {
	t.Helper()

	if got := h.Get("Access-Control-Expose-Headers"); got != "" {
		t.Fatalf("Access-Control-Expose-Headers on preflight = %q, want empty", got)
	}

	if got := h.Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, PATCH, DELETE" {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}

	if got := h.Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("Access-Control-Max-Age = %q, want 600", got)
	}

	allowHeaders := strings.Split(h.Get("Access-Control-Allow-Headers"), ", ")
	for _, want := range []string{"Authorization", "Content-Type", "Dustpan-Channel", "Idempotency-Key", "If-Match", "X-Dustpan-CSRF"} {
		if !slices.Contains(allowHeaders, want) {
			t.Fatalf("Access-Control-Allow-Headers = %q, want %q", allowHeaders, want)
		}
	}
}

func TestNewServerRefusesBadAllowedOrigins(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		origin string
		ok     bool
	}{
		{"https://console.example.com", true},
		{"http://localhost:5173", true},
		{"http://[::1]:8080", true},
		{"https://[2001:db8::1]:8443", true},
		{"http://127.0.0.1:3000", true},
		{"https://Console.example.com", false},
		{"https://bücher.example", false},
		{"https://console.example.com:443", false},
		{"http://console.example.com:80", false},
		{"http://console.example.com:", false},
		{"null", false},
		{"*", false},
		{"console.example.com", false},
		{"ftp://console.example.com", false},
		{"https://", false},
		{"https://console.example.com/", false},
		{"https://console.example.com/app", false},
		{"https://console.example.com?x=1", false},
		{"https://console.example.com#top", false},
		{"https://user@console.example.com", false},
		{"https://a.example;script-src", false},
		{"https://a.example,b.example", false},
		{"https://a.example b.example", false},
		{"https://a'.example", false},
		{"https://-a.example", false},
		{"https://a-.example", false},
		{"https://a..example", false},
		{"https://a_b.example", false},
		{"https://999.1.1.1", false},
		{"https://a.example:99999", false},
		{"https://a.example:0", false},
		{"https://a.example:080", false},
		{"https://a.example:8a", false},
		{"http://[::1%25eth0]:8080", false},
		{"https://xn--bcher-kva.example", true},
		{"https://a-b.example1:8443", true},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithAllowedOrigins(origin, tc.origin))

			_, err := dustpan.NewServer(opts...)
			if tc.ok && err != nil {
				t.Fatalf("origin %q: %v", tc.origin, err)
			}

			if !tc.ok {
				if !errors.Is(err, dustpan.ErrInvalidOption) || !strings.Contains(err.Error(), tc.origin) {
					t.Fatalf("origin %q: err = %v, want ErrInvalidOption naming the value", tc.origin, err)
				}
			}
		})
	}
}

func TestNewServerRequiresPorts(t *testing.T) {
	t.Parallel()

	if _, err := dustpan.NewServer(dustpan.WithInstallation(installationName)); err == nil {
		t.Fatal("NewServer without stores or authenticator succeeded")
	}

	st, blobs, authn := requireServerPorts(t)
	full := fullServerOptions(t, st, blobs, authn)

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
	full := fullServerOptions(t, st, blobs, authn)

	cases := []struct {
		name string
		drop int
	}{
		{name: "workspace store", drop: 8},
		{name: "state store", drop: 9},
		{name: "blob store", drop: 10},
		{name: "inventory reader", drop: 11},
		{name: "secret wrapper", drop: 12},
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

// TestNewServerRequiresSecretWrapper proves NewServer refuses to build a
// server without a key-encryption key: the error names WithSecretWrapper and
// matches ErrMissingOption (issue 367). The server never starts and fails
// only at the first sealed write.
func TestNewServerRequiresSecretWrapper(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	full := fullServerOptions(t, st, blobs, authn)

	_, err := dustpan.NewServer(full[:len(full)-1]...)
	if !errors.Is(err, dustpan.ErrMissingOption) {
		t.Fatalf("NewServer without a wrapper: err = %v, want ErrMissingOption", err)
	}

	if !strings.Contains(err.Error(), "WithSecretWrapper") {
		t.Fatalf("NewServer without a wrapper: err = %v, want it to name WithSecretWrapper", err)
	}

	srv, err := dustpan.NewServer(full...)
	if err != nil {
		t.Fatalf("NewServer with a wrapper: %v", err)
	}

	if srv == nil {
		t.Fatal("NewServer with a wrapper answered no server")
	}
}

// TestNewServerRefusesShortRetention proves that the SDK keeps the minimum
// soft-delete retention of the binary (ADR 47): a retention below one hour
// fails with ErrInvalidOption, while zero (the default) and one hour pass.
func TestNewServerRefusesShortRetention(t *testing.T) {
	t.Parallel()

	cases := []struct {
		retention time.Duration
		wantErr   bool
	}{
		{retention: time.Minute, wantErr: true},
		{retention: -time.Hour, wantErr: true},
		{retention: 0, wantErr: false},
		{retention: dustpan.MinSoftDeleteRetention, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.retention.String(), func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			opts := append(fullServerOptions(t, st, blobs, authn), dustpan.WithSoftDeleteRetention(tc.retention))

			_, err := dustpan.NewServer(opts...)
			if tc.wantErr != errors.Is(err, dustpan.ErrInvalidOption) {
				t.Fatalf(
					"NewServer with retention %s: err = %v, want ErrInvalidOption %v",
					tc.retention,
					err,
					tc.wantErr,
				)
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("NewServer with retention %s: %v", tc.retention, err)
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

// testWrapper answers a built-in key-encryption key wrapper over a fixed
// 32-byte test key. NewServer refuses to build without a wrapper.
func testWrapper(t *testing.T) *secret.BuiltinWrapper {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	return wrapper
}

// fullServerOptions returns every required NewServer option in a fixed order.
func fullServerOptions(
	t *testing.T, st *sqlite.Store, blobs *fs.Store, authn *identity.StaticToken,
) []dustpan.Option {
	t.Helper()

	return []dustpan.Option{
		dustpan.WithInstallation(installationName),
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
		dustpan.WithSecretWrapper(testWrapper(t)),
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

// TestRunRefusesMismatchedKey proves Run runs the key-encryption key
// start-up check itself (ADR 23): a program that embeds the server and never
// calls secret.CheckWrapper still refuses to start with the wrong key.
func TestRunRefusesMismatchedKey(t *testing.T) {
	t.Parallel()

	st := seedRunCheckStore(t)
	srv := newRunCheckServer(t, st)

	runErr := srv.Run(t.Context())
	if runErr == nil || !errors.Is(runErr, secret.ErrWrapperMismatch) {
		t.Fatalf("run with another key: err = %v, want ErrWrapperMismatch", runErr)
	}

	if !strings.Contains(runErr.Error(), "rewrap") {
		t.Fatalf("run with another key: err = %v, want it to name the rewrap command", runErr)
	}
}

// seedRunCheckStore opens a SQLite store with one wrapped key under the
// right key-encryption key.
func seedRunCheckStore(t *testing.T) *sqlite.Store {
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

	namespaces, nsErr := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: installationName})
	if nsErr != nil {
		t.Fatalf("namespace service: %v", nsErr)
	}

	createErr := st.CreateNamespace(
		t.Context(),
		namespace.Namespace{
			ID:                  namespace.NewID(),
			CreatedAt:           time.Now(),
			Name:                "acme",
			PrimaryInstallation: installationName,
			FailoverVersion:     1,
			Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		},
	)
	if createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	right, wrapErr := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if wrapErr != nil {
		t.Fatalf("new wrapper: %v", wrapErr)
	}

	secrets, svcErr := secret.NewService(secret.Config{
		Keys:          st,
		Wrapper:       right,
		Namespaces:    namespaces,
		Now:           nil,
		MaxBatchBytes: 0,
	})
	if svcErr != nil {
		t.Fatalf("secret service: %v", svcErr)
	}

	sc, scopeErr := secret.NewScope("acme", "stk-runcheck00000000001")
	if scopeErr != nil {
		t.Fatalf("new scope: %v", scopeErr)
	}

	if _, encErr := secrets.Encrypt(t.Context(), sc, [][]byte{[]byte("s3cr3t")}); encErr != nil {
		t.Fatalf("seed wrapped key: %v", encErr)
	}

	return st
}

// newRunCheckServer wires a server over st with another key-encryption key
// than the one that wrapped its keys.
func newRunCheckServer(t *testing.T, st *sqlite.Store, extra ...dustpan.Option) *dustpan.Server {
	t.Helper()

	wrong, err := secret.NewBuiltinWrapper([]byte("fedcba9876543210fedcba9876543210"))
	if err != nil {
		t.Fatalf("new other wrapper: %v", err)
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

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	opts := []dustpan.Option{
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrong),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithListenAddr("127.0.0.1:0"),
	}

	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
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
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(testWrapper(t)),
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

	ts := httptest.NewServer(startedHandler(t, srv))
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
		{
			"unknown v1 path is a JSON 404",
			http.MethodGet,
			"/api/dustpan/v1/nothing",
			adminToken,
			`"code":404`,
			http.StatusNotFound,
		},
		{
			"unknown v1 write is a JSON 404",
			http.MethodPost,
			"/api/dustpan/v1/nothing",
			adminToken,
			`"code":404`,
			http.StatusNotFound,
		},
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

// openBackupTarget opens a file-system backup target under a temporary
// directory, for the backup wiring tests.
func openBackupTarget(t *testing.T) *fs.Store {
	t.Helper()

	target, err := fs.Open(filepath.Join(t.TempDir(), "backup-target"))
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	return target
}

func TestNewServerWithoutBackupTargetBuildsNoBackupService(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)

	srv, err := dustpan.NewServer(fullServerOptions(t, st, blobs, authn)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	if srv.Backups() != nil {
		t.Fatal("server without a backup target builds a backup service, want nil")
	}
}

func TestBackupTargetS3SetsObjectLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		targetName string
		want       int64
	}{
		{name: "s3 sets the limit", targetName: "s3://b/p", want: backup.S3ObjectLimit},
		{name: "s3 with spaces sets the limit", targetName: "  s3://b/p  ", want: backup.S3ObjectLimit},
		{name: "dir keeps no limit", targetName: "dir:/var/backups", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			target := openBackupTarget(t)

			srv, err := dustpan.NewServer(append(
				fullServerOptions(t, st, blobs, authn),
				dustpan.WithBackupTarget(target, tc.targetName),
			)...)
			if err != nil {
				t.Fatalf("new server: %v", err)
			}

			service := srv.Backups()
			if service == nil {
				t.Fatal("server with a backup target builds no backup service")
			}

			if got := service.MaxSQLBytes(); got != tc.want {
				t.Fatalf("backup SQL limit for %q = %d, want %d", tc.targetName, got, tc.want)
			}
		})
	}
}

// TestBackupTargetComesOnlyFromTheOption proves WithBackupTarget is the only
// way the target gets in: a request that names another target in the query, a
// header and the body leaves the service and the other directory untouched.
func TestBackupTargetComesOnlyFromTheOption(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	target := openBackupTarget(t)

	srv, err := dustpan.NewServer(append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithBackupTarget(target, "dir:/var/backups"),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	before := srv.Backups()
	if before == nil {
		t.Fatal("server with a backup target builds no backup service")
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	evil := filepath.Join(t.TempDir(), "evil")
	sendEvilTarget(t, ts.URL, evil)

	if srv.Backups() != before {
		t.Fatal("a request changed the backup service, want the option to stay the only way in")
	}

	if _, statErr := os.Stat(evil); !os.IsNotExist(statErr) {
		t.Fatalf("request reached %s, want the target unchanged", evil)
	}
}

// sendEvilTarget sends a request that names another backup target in the
// query, a header and the body. No route reads it.
func sendEvilTarget(t *testing.T, base, evil string) {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		base+"/api/server/backups?target="+evil,
		strings.NewReader(`{"target":"dir:`+evil+`"}`),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)
	req.Header.Set("X-Backup-Target", "dir:"+evil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post evil target: %v", err)
	}

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}
}

// TestRunLeavesRunningBackupAlone proves that a start writes no backup row:
// a running row, which another instance may run, reads running with no
// error text after the start-up steps ended.
func TestRunLeavesRunningBackupAlone(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	target := openBackupTarget(t)

	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	seedRunningBackup(t, st, now)

	started := make(chan struct{})

	srv, err := dustpan.NewServer(append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithBackupTarget(target, "dir:/var/backups"),
		dustpan.WithClock(func() time.Time { return now }),
		dustpan.WithListenAddr("127.0.0.1:0"),
		dustpan.WithBeforeServe(func(context.Context) error {
			close(started)

			return nil
		}),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- srv.Run(ctx)
	}()

	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not finish its start-up steps")
	}

	row, err := st.GetBackup(t.Context(), "bkp-restart00000000001")
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}

	if row.State != backup.StateRunning || row.Error != "" || !row.EndedAt.IsZero() {
		t.Fatalf("backup after start = %+v, want the running row unchanged", row)
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("run server: %v", err)
	}
}

// TestRunReturnsNilWhenCanceledBeforeStart proves Run treats a done ctx as a
// stop, not a failure: with ctx canceled before Run starts, the start-up
// steps (key check, running backups) see the canceled ctx and Run returns
// nil.
func TestRunReturnsNilWhenCanceledBeforeStart(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	target := openBackupTarget(t)

	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	seedRunningBackup(t, st, now)

	srv, err := dustpan.NewServer(append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithBackupTarget(target, "dir:/var/backups"),
		dustpan.WithClock(func() time.Time { return now }),
		dustpan.WithListenAddr("127.0.0.1:0"),
	)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := srv.Run(ctx); err != nil {
		t.Fatalf("run server with a canceled context: %v, want nil", err)
	}
}

// seedRunningBackup stores one running backup row, as a stop during a backup leaves it.
func seedRunningBackup(t *testing.T, st *sqlite.Store, now time.Time) {
	t.Helper()

	running := backup.Record{
		StartedAt: now,
		EndedAt:   time.Time{},
		ID:        "bkp-restart00000000001",
		Target:    "dir:/var/backups",
		Error:     "",
		State:     backup.StateRunning,
	}

	if err := st.CreateBackup(t.Context(), running); err != nil {
		t.Fatalf("create running backup: %v", err)
	}
}

// The three stores below shadow one port of the dev data rules of the SQLite
// store with a method of another signature, so the store holds every other
// port and fails the type assertion to the missing one. The strip runs in
// the stage of RestoreStaging, and the report goes to the row through
// RestoreOperations.
type (
	noKindReaderStore struct{ *sqlite.Store }
	noStagingStore    struct{ *sqlite.Store }
	noOperationsStore struct{ *sqlite.Store }
)

func (noKindReaderStore) ReadKind() {}

func (noStagingStore) StageRestore() {}

func (noOperationsStore) RecordRestoreReport() {}

func TestServerWithoutDevPortsDoesNotStart(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		wrap    func(*sqlite.Store) namespace.Store
		missing string
	}{
		{name: "no KindReader", wrap: func(s *sqlite.Store) namespace.Store { return noKindReaderStore{s} }, missing: "KindReader"},
		{name: "no RestoreStaging", wrap: func(s *sqlite.Store) namespace.Store { return noStagingStore{s} }, missing: "RestoreStaging"},
		{
			name: "no RestoreOperations", wrap: func(s *sqlite.Store) namespace.Store { return noOperationsStore{s} },
			missing: "RestoreOperations",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			target := openBackupTarget(t)

			srv, err := dustpan.NewServer(append(
				fullServerOptions(t, st, blobs, authn),
				dustpan.WithNamespaceStore(tc.wrap(st)),
				dustpan.WithBackupTarget(target, "dir:/var/backups"),
			)...)
			if err == nil {
				t.Fatalf(
					"NewServer with a store that lacks %s started, want a start error (server %v)",
					tc.missing,
					srv,
				)
			}

			if !strings.Contains(err.Error(), "lacks "+tc.missing) {
				t.Fatalf("start error = %q, want it to name %s", err, tc.missing)
			}
		})
	}
}
