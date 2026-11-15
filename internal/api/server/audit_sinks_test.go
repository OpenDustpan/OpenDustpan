package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// stubAuditSinks lists a fixed set of sink statuses.
type stubAuditSinks struct {
	statuses []audit.SinkStatus
	err      error
}

func (s stubAuditSinks) ListAuditSinks(context.Context) ([]audit.SinkStatus, error) {
	if s.err != nil {
		return nil, s.err
	}

	return append([]audit.SinkStatus(nil), s.statuses...), nil
}

// newAuditSinkHandler builds a Handler with the given sink source and the
// AdminOnly authorizer, so the tests prove the production policy.
func newAuditSinkHandler(t *testing.T, source apiserver.AuditSinkSource) testHandler {
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
		TempAdmins:       nil,
		Now:              nil,
		Authenticator:    adminAuthenticator{},
		Authorizer:       identity.AdminOnly{},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           nil,
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		AuditChain:       nil,
		AuditSinks:       source,
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

func auditSinkRequest(t *testing.T, token string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/audit/sinks", http.NoBody)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	return req
}

func decodeAuditSinks(t *testing.T, body string) []auditSinkItem {
	t.Helper()

	var got struct {
		Sinks []auditSinkItem `json:"sinks"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode sinks: %v (%s)", err, body)
	}

	return got.Sinks
}

// auditSinkItem decodes one row of the sink list.
type auditSinkItem struct {
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	Cursor     map[string]int64 `json:"cursor"`
	LagSeconds float64          `json:"lagSeconds"`
	LastError  string           `json:"lastError"`
}

// TestAuditSinkRouteDeclaresObserve proves the sink list declares the
// server:observe action on the installation object.
func TestAuditSinkRouteDeclaresObserve(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	for _, route := range handler.Routes() {
		if route.Method != http.MethodGet {
			continue
		}

		if route.Path != "/api/server/audit/sinks" {
			continue
		}

		checkAuditSinkRoute(t, route)

		return
	}

	t.Fatal("no GET /api/server/audit/sinks route")
}

// checkAuditSinkRoute proves one route row declares server:observe on the
// installation object.
func checkAuditSinkRoute(t *testing.T, route apiserver.Route) {
	t.Helper()

	if route.Scope != apiserver.ScopeAuthorized {
		t.Fatalf("sink route scope = %v, want authorized", route.Scope)
	}

	if len(route.Actions) != 1 || route.Actions[0] != identity.ActionServerObserve {
		t.Fatalf("sink route actions = %v, want [server:observe]", route.Actions)
	}

	if route.Resolve == nil {
		t.Fatal("sink route declares no resolver")
	}

	obj, err := route.Resolve(nil, nil)
	if err != nil {
		t.Fatalf("resolve sink object: %v", err)
	}

	if !obj.IsServer() {
		t.Fatalf("sink object = %q, want the installation", obj.String())
	}
}

// TestListAuditSinksEmpty proves the list is empty with no sink configured.
func TestListAuditSinksEmpty(t *testing.T) {
	t.Parallel()

	handler := newAuditSinkHandler(t, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, auditSinkRequest(t, "admin-token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("sink list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	if got := decodeAuditSinks(t, rec.Body.String()); len(got) != 0 {
		t.Fatalf("sink list = %+v, want none", got)
	}
}

// TestListAuditSinksOneSink proves the list holds one configured sink with
// its cursor, lag and error.
func TestListAuditSinksOneSink(t *testing.T) {
	t.Parallel()

	source := stubAuditSinks{
		statuses: []audit.SinkStatus{
			{
				Name:       "file",
				Kind:       "file",
				Cursors:    map[string]int64{"acme": 7, "@server": 2},
				LagSeconds: 12.5,
				LastError:  "",
			},
		},
		err: nil,
	}
	handler := newAuditSinkHandler(t, source)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, auditSinkRequest(t, "admin-token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("sink list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	got := decodeAuditSinks(t, rec.Body.String())
	if len(got) != 1 {
		t.Fatalf("sink list holds %d sinks, want 1 (%s)", len(got), rec.Body.String())
	}

	sink := got[0]
	if sink.Name != "file" || sink.Kind != "file" {
		t.Fatalf("sink = %+v, want name file and kind file", sink)
	}

	if sink.Cursor["acme"] != 7 || sink.Cursor["@server"] != 2 {
		t.Fatalf("sink cursor = %+v, want acme 7 and @server 2", sink.Cursor)
	}

	if sink.LagSeconds != 12.5 {
		t.Fatalf("sink lag = %v, want 12.5", sink.LagSeconds)
	}

	if sink.LastError != "" {
		t.Fatalf("sink error = %q, want empty", sink.LastError)
	}
}
