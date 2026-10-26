package sourcefilter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/sourcefilter"
	"opendustpan.org/opendustpan/internal/source"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	adminToken = "admin-token"
	region     = "eu-west"
)

// sourceFilterURL is the path of the source filter of the namespace acme.
const sourceFilterURL = "/api/dustpan/v1/namespaces/acme/source-filter"

// stores are the ports of one SQL store that the tests use.
type stores struct {
	namespaces namespace.Store
	filters    source.Filters
	audit      interface {
		List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error)
	}
}

// backend opens an empty store of one SQL dialect.
type backend struct {
	name string
	open func(t *testing.T) stores
}

// backends returns SQLite, and PostgreSQL when DUSTPAN_TEST_POSTGRES_URL is set.
func backends() []backend {
	list := []backend{{name: "sqlite", open: openSQLite}}

	if os.Getenv(postgresEnv) != "" {
		list = append(list, backend{name: "postgres", open: openPostgres})
	}

	return list
}

func openSQLite(t *testing.T) stores {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return stores{namespaces: s, filters: s.SourceFilters(), audit: s}
}

// adminAuthenticator signs in the administrator ada for adminToken.
type adminAuthenticator struct{}

func (adminAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != adminToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: "ada", DisplayName: "ada", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}, nil
}

// holdsOnly allows the actions it lists on a namespace, and denies the rest.
type holdsOnly struct {
	actions []identity.Action
}

func (h holdsOnly) Authorize(_ context.Context, _ identity.Principal, action identity.Action, _ identity.Object) error {
	if slices.Contains(h.actions, action) {
		return nil
	}

	return identity.ErrDenied
}

var errNoListing = errors.New("no route lists objects")

func (holdsOnly) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// env is a store with the handler over it, on a test server, with the
// namespace acme.
type env struct {
	t       *testing.T
	store   stores
	server  *httptest.Server
	handler *sourcefilter.Handler
}

// serve returns a handler over s on a test server. The authorizer decides each
// check, and the administrator token signs in the principal.
func serve(t *testing.T, s stores, authz identity.Authorizer) (*sourcefilter.Handler, *httptest.Server) {
	t.Helper()

	service, err := namespace.NewService(namespace.Config{Store: s.namespaces, Now: nil, Region: region})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	h, err := sourcefilter.NewHandler(sourcefilter.Config{
		Namespaces: service, Filters: s.filters, Authenticator: adminAuthenticator{}, Authorizer: authz,
		Audit: newTestAudit(), OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil,
		Logger: slog.New(slog.DiscardHandler), Now: nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return h, srv
}

// newEnv opens a store with open, creates the namespace acme, and serves the
// handler with the authorizer authz.
func newEnv(t *testing.T, open func(t *testing.T) stores, authz identity.Authorizer) *env {
	t.Helper()

	s := open(t)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme", HomeRegion: region,
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.namespaces.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	h, srv := serve(t, s, authz)

	return &env{t: t, store: s, server: srv, handler: h}
}

// raw sends a request with a body that the test gives as text, as the
// administrator.
func (e *env) raw(method, path, body string) (int, []byte) {
	e.t.Helper()

	req, err := http.NewRequestWithContext(e.t.Context(), method, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)

	return e.send(req)
}

func (e *env) send(req *http.Request) (int, []byte) {
	e.t.Helper()

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("send %s %s: %v", req.Method, req.URL.Path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			e.t.Errorf("close body: %v", closeErr)
		}
	}()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, out
}

func decode(t *testing.T, body []byte, v any) {
	t.Helper()

	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}
