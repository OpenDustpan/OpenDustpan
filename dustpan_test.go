package dustpan_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dustpan "opendustpan.org/opendustpan"
	"opendustpan.org/opendustpan/identity"
	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/store/sqlite"
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

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithAllowedOrigins(origin),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return fixture{store: st, server: ts}
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

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithConsoleDir(dir),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	f := fixture{store: st, server: ts}

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
