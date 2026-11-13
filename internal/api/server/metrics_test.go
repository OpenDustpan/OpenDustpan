package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// adminAuthenticator signs in exactly the admin token as an administrator.
type adminAuthenticator struct{}

func (adminAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "admin-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "admin",
		DisplayName:    "Admin",
		Admin:          true,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}, nil
}

// expositionWriter writes one fixed exposition, so the format test proves
// the route passes the registry output through untouched.
type expositionWriter struct{}

func (expositionWriter) Write(w io.Writer) error {
	const exposition = "# HELP dustpan_http_requests_total HTTP requests by adapter.\n" +
		"# TYPE dustpan_http_requests_total counter\n" +
		"dustpan_http_requests_total{adapter=\"pulumi\"} 1\n"

	_, err := io.WriteString(w, exposition)

	return err //nolint:wrapcheck // Transparent writer: callers must see the original error.
}

// newMetricsHandler builds a Handler with the given authenticator and the
// AdminOnly authorizer, so the metrics tests prove the production policy.
func newMetricsHandler(
	t *testing.T,
	authn identity.Authenticator,
	metrics apiserver.MetricsWriter,
) testHandler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: stubNamespaceStore{}, Now: nil, Installation: "test"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

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
		Authenticator:    authn,
		Authorizer:       identity.AdminOnly{},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          metrics,
		Version:          "test",
		Schema:           nil,
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

// metricsRequest builds a GET request for the metrics route. An empty token
// sends no Authorization header.
func metricsRequest(t *testing.T, token string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/metrics", http.NoBody)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return req
}

// TestMetricsRouteDeclaresObserve proves the metrics route declares the
// server:observe action on the installation object.
func TestMetricsRouteDeclaresObserve(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	for _, route := range handler.Routes() {
		if route.Method == http.MethodGet && route.Path == "/api/server/metrics" {
			checkMetricsRoute(t, route)

			return
		}
	}

	t.Fatal("no GET /api/server/metrics route")
}

// checkMetricsRoute proves one route row declares server:observe on the
// installation object.
func checkMetricsRoute(t *testing.T, route apiserver.Route) {
	t.Helper()

	if route.Scope != apiserver.ScopeAuthorized {
		t.Fatalf("metrics route scope = %v, want authorized", route.Scope)
	}

	if len(route.Actions) != 1 || route.Actions[0] != identity.ActionServerObserve {
		t.Fatalf("metrics route actions = %v, want [server:observe]", route.Actions)
	}

	if route.Resolve == nil {
		t.Fatal("metrics route declares no resolver")
	}

	obj, err := route.Resolve(nil, nil)
	if err != nil {
		t.Fatalf("resolve metrics object: %v", err)
	}

	if !obj.IsServer() {
		t.Fatalf("metrics object = %q, want the installation", obj.String())
	}
}

// TestMetricsWithoutTokenAnswers401 proves a request without a token gets
// 401.
func TestMetricsWithoutTokenAnswers401(t *testing.T) {
	t.Parallel()

	handler := newMetricsHandler(t, adminAuthenticator{}, expositionWriter{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, metricsRequest(t, ""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf(
			"metrics without a token answers %d, want %d (%s)",
			rec.Code,
			http.StatusUnauthorized,
			rec.Body.String(),
		)
	}
}

// TestMetricsForNonAdminAnswers403 proves a signed-in principal without the
// administrator right gets 403.
func TestMetricsForNonAdminAnswers403(t *testing.T) {
	t.Parallel()

	handler := newMetricsHandler(t, tokenAuthenticator{}, expositionWriter{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, metricsRequest(t, "test-token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf(
			"metrics for a non-administrator answers %d, want %d (%s)",
			rec.Code,
			http.StatusForbidden,
			rec.Body.String(),
		)
	}
}

// TestMetricsServesFormat proves an administrator gets 200 with the registry
// in the Prometheus text format.
func TestMetricsServesFormat(t *testing.T) {
	t.Parallel()

	handler := newMetricsHandler(t, adminAuthenticator{}, expositionWriter{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, metricsRequest(t, "admin-token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics for an administrator answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "text/plain; version=0.0.4" {
		t.Fatalf("media type = %q, want %q", got, "text/plain; version=0.0.4")
	}

	for _, want := range []string{
		"# HELP dustpan_http_requests_total",
		"# TYPE dustpan_http_requests_total counter",
		"dustpan_http_requests_total{adapter=\"pulumi\"} 1",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("metrics body misses %q:\n%s", want, rec.Body.String())
		}
	}
}

// SetOverrides keeps no switch: the stub only names the namespace acme.
func (stubNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
