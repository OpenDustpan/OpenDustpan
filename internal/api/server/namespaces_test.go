package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// namespaceDoc decodes a created namespace in tests.
type namespaceDoc struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	PrimaryInstallation string `json:"primaryInstallation"`
	FailoverVersion     int64  `json:"failoverVersion"`
	CreatedAt           string `json:"createdAt"`
}

// newNamespaceHandler builds a Handler with a SQLite-backed namespace service
// over a fresh store on installation test. The store starts empty, so the first
// create proves the bootstrap call on an empty installation.
func newNamespaceHandler(t *testing.T, authz identity.Authorizer) testHandler {
	t.Helper()

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       authz,
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       newTestNamespaces(t),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// newTestNamespaces returns a namespace service on installation test over a fresh
// SQLite store that the test closes at cleanup.
func newTestNamespaces(t *testing.T) *namespace.Service {
	t.Helper()

	st := openServerTemplateStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	return namespaces
}

// serveNamespace sends one namespace create request with the test token and
// returns its status and body.
func serveNamespace(t *testing.T, handler testHandler, body string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/server/namespaces",
		strings.NewReader(body),
	)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// TestNamespaceRouteDeclaresCreate proves the namespace create route
// declares exactly the action server:create-namespace, with a resolver that
// returns the installation (ADR 52).
func TestNamespaceRouteDeclaresCreate(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	route, ok := findNamespaceRoute(t, handler)
	if !ok {
		t.Fatal("no POST /api/server/namespaces route")
	}

	checkNamespaceRouteCreate(t, route)
}

// findNamespaceRoute returns the namespace create route, or false when the
// table holds none.
func findNamespaceRoute(
	t *testing.T, handler testHandler,
) (apiserver.Route, bool) {
	t.Helper()

	for _, route := range handler.Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/server/namespaces" {
			return route, true
		}
	}

	//nolint:exhaustruct_v5 // No route to return, so the zero value signals absence with false.
	return apiserver.Route{}, false
}

// checkNamespaceRouteCreate proves one route row declares
// server:create-namespace on the installation.
func checkNamespaceRouteCreate(t *testing.T, route apiserver.Route) {
	t.Helper()

	if route.Scope != apiserver.ScopeAuthorized {
		t.Fatalf("route scope = %d, want authorized", route.Scope)
	}

	if len(route.Actions) != 1 || route.Actions[0] != identity.ActionServerCreateNamespace {
		t.Fatalf("route actions = %v, want [server:create-namespace]", route.Actions)
	}

	if route.Resolve == nil {
		t.Fatal("route declares actions but no resolver")
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/server/namespaces",
		strings.NewReader(`{"name":"acme"}`),
	)

	obj, err := route.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if !obj.IsServer() {
		t.Fatalf("resolver returns %q, want the installation", obj.String())
	}
}

// TestNamespaceCreateOnEmpty proves the bootstrap call on an empty
// installation creates a namespace with the installation of the server as primary installation and
// answers 201 with it.
func TestNamespaceCreateOnEmpty(t *testing.T) {
	t.Parallel()

	handler := newNamespaceHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	status, body := serveNamespace(t, handler, `{"name":"acme"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST namespaces answers %d, want %d (%s)", status, http.StatusCreated, body)
	}

	var doc namespaceDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode namespace %q: %v", body, err)
	}

	if doc.Name != "acme" || doc.PrimaryInstallation != "test" || doc.FailoverVersion != 1 || doc.CreatedAt == "" {
		t.Fatalf("namespace answers %+v, want acme in test at version 1 with a time", doc)
	}

	if _, err := namespace.ParseID(doc.ID); err != nil {
		t.Fatalf("namespace answers ID %q: %v", doc.ID, err)
	}
}

// TestNamespaceCreateDuplicate proves a second create of the same name
// answers 409 with the one JSON error shape.
func TestNamespaceCreateDuplicate(t *testing.T) {
	t.Parallel()

	handler := newNamespaceHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	if status, body := serveNamespace(t, handler, `{"name":"acme"}`); status != http.StatusCreated {
		t.Fatalf("first POST answers %d, want %d (%s)", status, http.StatusCreated, body)
	}

	status, body := serveNamespace(t, handler, `{"name":"acme"}`)
	if status != http.StatusConflict {
		t.Fatalf("second POST answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	if doc := decodeError(t, body); doc.Code != http.StatusConflict || doc.Message == "" {
		t.Fatalf("duplicate error = %+v, want code 409 with a message", doc)
	}
}

// TestNamespaceCreateInvalid proves a bad name and a bad body answer 400 with
// the one JSON error shape.
func TestNamespaceCreateInvalid(t *testing.T) {
	t.Parallel()

	handler := newNamespaceHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	for _, body := range []string{`{"name":"../x"}`, `{}`, `not json`} {
		status, resp := serveNamespace(t, handler, body)
		if status != http.StatusBadRequest {
			t.Fatalf("POST %q answers %d, want %d (%s)", body, status, http.StatusBadRequest, resp)
		}

		if doc := decodeError(t, resp); doc.Code != http.StatusBadRequest || doc.Message == "" {
			t.Fatalf("POST %q error = %+v, want code 400 with a message", body, doc)
		}
	}
}

// TestNamespaceCreateNeedsToken proves the route answers 401 without a token,
// with the one JSON error shape.
func TestNamespaceCreateNeedsToken(t *testing.T) {
	t.Parallel()

	handler := newNamespaceHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/server/namespaces",
		strings.NewReader(`{"name":"acme"}`),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST without a token answers %d, want %d (%s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	if doc := decodeError(t, rec.Body.String()); doc.Code != http.StatusUnauthorized || doc.Message == "" {
		t.Fatalf("missing token error = %+v, want code 401 with a message", doc)
	}
}

// TestNamespaceCreateDenied proves a caller without server:create-namespace
// answers 403.
func TestNamespaceCreateDenied(t *testing.T) {
	t.Parallel()

	handler := newNamespaceHandler(
		t,
		selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionServerCreateNamespace: true}},
	)

	status, body := serveNamespace(t, handler, `{"name":"acme"}`)
	if status != http.StatusForbidden {
		t.Fatalf("denied POST answers %d, want %d (%s)", status, http.StatusForbidden, body)
	}
}

// TestNamespaceBodyTooLarge proves that a create body over the limit of the
// namespace route answers 413 request.body-too-large with the limit, and that
// a body of exactly the limit is read.
func TestNamespaceBodyTooLarge(t *testing.T) {
	t.Parallel()

	handler := newNamespaceHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	const limit = 8192

	// The padding is spaces after the name, so the body is valid JSON and only
	// its size decides the answer.
	exact := `{"name":"acme"}` + strings.Repeat(" ", limit-len(`{"name":"acme"}`))
	if status, body := serveNamespace(t, handler, exact); status == http.StatusRequestEntityTooLarge {
		t.Fatalf("body of %d bytes = %d %s, want it read", limit, status, body)
	}

	over := `{"name":"acme"}` + strings.Repeat(" ", limit-len(`{"name":"acme"}`)+1)

	status, body := serveNamespace(t, handler, over)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("body of %d bytes = %d %s, want 413", limit+1, status, body)
	}

	var got apiError
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("answer %q is not an error: %v", body, err)
	}

	if got.ErrorCode != "request.body-too-large" || got.ErrorArgs["maxBytes"] != float64(limit) {
		t.Fatalf("answer = %+v, want request.body-too-large with maxBytes %d", got, limit)
	}
}
