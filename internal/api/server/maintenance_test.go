package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// fakeMaintenance stubs the maintenance service: it answers state, cancels
// canceled on enter, and fails with enterErr, getErr or leaveErr. It records
// the force flag of each enter.
type fakeMaintenance struct {
	state    maintenance.State
	canceled []string
	enterErr error
	getErr   error
	leaveErr error
	forces   []bool
}

func (f *fakeMaintenance) Get(context.Context) (maintenance.State, error) {
	if f.getErr != nil {
		return "", f.getErr
	}

	return f.state, nil
}

func (f *fakeMaintenance) Enter(_ context.Context, force bool) (maintenance.EnterResult, error) {
	f.forces = append(f.forces, force)

	if f.enterErr != nil {
		return maintenance.EnterResult{}, f.enterErr
	}

	f.state = maintenance.StateMaintenance

	return maintenance.EnterResult{Canceled: f.canceled}, nil
}

func (f *fakeMaintenance) Leave(context.Context) error {
	if f.leaveErr != nil {
		return f.leaveErr
	}

	f.state = maintenance.StateNormal

	return nil
}

// newFakeMaintenance returns a fake that answers state with no canceled
// work and no failures. Tests set the other fields after it.
func newFakeMaintenance(state maintenance.State) *fakeMaintenance {
	return &fakeMaintenance{
		state:    state,
		canceled: nil,
		enterErr: nil,
		getErr:   nil,
		leaveErr: nil,
		forces:   nil,
	}
}

// newMaintenanceHandler builds a Handler with fakeMaintenance, so the
// maintenance tests drive the routes without a database.
func newMaintenanceHandler(t *testing.T, fake *fakeMaintenance) *apiserver.Handler {
	t.Helper()

	handler, err := apiserver.NewHandler(apiserver.Config{
		Preferences:      nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       newTestNamespaces(t),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      fake,
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            &testAudit{},
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
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// serveMaintenance sends one installation API request with the test token and
// returns its status and body.
func serveMaintenance(
	t *testing.T, handler *apiserver.Handler, method, path, body string,
) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// maintenanceState decodes a state response body in tests.
type maintenanceState struct {
	State string `json:"state"`
}

// enterState decodes an enter response body in tests.
type enterState struct {
	State    string   `json:"state"`
	Canceled []string `json:"canceled"`
}

func TestMaintenanceRoutesDeclareMaintenance(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	want := map[string]bool{
		"GET /api/server/maintenance":        false,
		"POST /api/server/maintenance/enter": false,
		"POST /api/server/maintenance/leave": false,
	}

	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, ok := want[pattern]; !ok {
			continue
		}

		want[pattern] = true

		if route.Scope != apiserver.ScopeAuthorized {
			t.Errorf("route %q scope = %v, want authorized", pattern, route.Scope)
		}

		if len(route.Actions) != 1 || route.Actions[0] != identity.ActionServerMaintenance {
			t.Errorf("route %q actions = %v, want [server:maintenance]", pattern, route.Actions)
		}

		if route.Resolve == nil {
			t.Errorf("route %q declares no resolver", pattern)
		}
	}

	for pattern, found := range want {
		if !found {
			t.Errorf("no %q route", pattern)
		}
	}
}

func TestGetMaintenanceAnswersState(t *testing.T) {
	t.Parallel()

	handler := newMaintenanceHandler(t, newFakeMaintenance(maintenance.StateNormal))

	status, body := serveMaintenance(t, handler, http.MethodGet, "/api/server/maintenance", "")
	if status != http.StatusOK {
		t.Fatalf("GET maintenance answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc maintenanceState

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode maintenance state %q: %v", body, err)
	}

	if doc.State != "normal" {
		t.Fatalf("maintenance state = %q, want normal", doc.State)
	}
}

func TestEnterMaintenanceWithoutForce(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateNormal)
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/maintenance/enter", "")
	if status != http.StatusOK {
		t.Fatalf("POST enter answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc enterState

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode enter %q: %v", body, err)
	}

	if doc.State != "maintenance" {
		t.Fatalf("enter state = %q, want maintenance", doc.State)
	}

	if doc.Canceled == nil || len(doc.Canceled) != 0 {
		t.Fatalf("enter canceled = %v, want an empty list, never null", doc.Canceled)
	}

	if len(fake.forces) != 1 || fake.forces[0] {
		t.Fatalf("enter force flags = %v, want one enter without force", fake.forces)
	}
}

func TestEnterMaintenanceWithForceNamesCanceled(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateNormal)
	fake.canceled = []string{"pulumi:acme/web/prod"}
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(
		t, handler, http.MethodPost, "/api/server/maintenance/enter", `{"force":true}`,
	)
	if status != http.StatusOK {
		t.Fatalf("POST enter answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc enterState

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode enter %q: %v", body, err)
	}

	if len(doc.Canceled) != 1 || doc.Canceled[0] != "pulumi:acme/web/prod" {
		t.Fatalf("enter canceled = %v, want [pulumi:acme/web/prod]", doc.Canceled)
	}

	if len(fake.forces) != 1 || !fake.forces[0] {
		t.Fatalf("enter force flags = %v, want one enter with force", fake.forces)
	}
}

func TestEnterMaintenanceRefusesActiveWork(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateNormal)
	fake.enterErr = fmt.Errorf("%w: %s", maintenance.ErrActiveWork, "pulumi:acme/web/prod")
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/maintenance/enter", "")
	if status != http.StatusConflict {
		t.Fatalf("POST enter with active work answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	var doc struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode 409 of POST enter: %v (%s)", err, body)
	}

	if doc.Code != http.StatusConflict || !strings.Contains(doc.Message, "pulumi:acme/web/prod") {
		t.Fatalf("POST enter error = %+v, want code 409 naming the active update", doc)
	}
}

func TestEnterMaintenanceListsCanceledWorkWhenForceGivesUp(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateNormal)
	fake.enterErr = fmt.Errorf("enter: %w", &maintenance.ActiveWorkError{
		Work: []string{"pulumi:acme/web/racer3"}, Canceled: []string{"pulumi:acme/web/prod"},
	})
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/maintenance/enter", `{"force":true}`)
	if status != http.StatusConflict {
		t.Fatalf("POST forced enter that gave up answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	var doc struct {
		Code     int      `json:"code"`
		Message  string   `json:"message"`
		Canceled []string `json:"canceled"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode 409 of POST enter: %v (%s)", err, body)
	}

	if doc.Code != http.StatusConflict || !strings.Contains(doc.Message, "pulumi:acme/web/racer3") ||
		len(doc.Canceled) != 1 || doc.Canceled[0] != "pulumi:acme/web/prod" {
		t.Fatalf("POST enter error = %+v, want code 409 naming the blocking and the canceled work", doc)
	}
}

func TestEnterMaintenanceListsCanceledWorkOnOtherFailures(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateNormal)
	fake.enterErr = fmt.Errorf("enter: %w", &maintenance.CanceledError{
		Err: maintenance.ErrUnavailable, Canceled: []string{"pulumi:acme/web/prod"},
	})
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/maintenance/enter", `{"force":true}`)
	if status != http.StatusInternalServerError {
		t.Fatalf("POST forced enter that failed answers %d, want %d (%s)", status, http.StatusInternalServerError, body)
	}

	var doc struct {
		Code     int      `json:"code"`
		Canceled []string `json:"canceled"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode error of POST enter: %v (%s)", err, body)
	}

	if doc.Code != http.StatusInternalServerError || len(doc.Canceled) != 1 ||
		doc.Canceled[0] != "pulumi:acme/web/prod" {
		t.Fatalf("POST enter error = %+v, want code 500 naming the canceled work", doc)
	}
}

func TestEnterMaintenanceRejectsBadBody(t *testing.T) {
	t.Parallel()

	handler := newMaintenanceHandler(t, newFakeMaintenance(maintenance.StateNormal))

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/maintenance/enter", `{`)
	if status != http.StatusBadRequest {
		t.Fatalf("POST enter with a bad body answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}

	var doc struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode 400 of POST enter: %v (%s)", err, body)
	}

	if doc.Code != http.StatusBadRequest || doc.Message == "" {
		t.Fatalf("POST enter error = %+v, want code 400 with a message", doc)
	}
}

// TestEnterMaintenanceRejectsOversizedBody proves a body larger than the
// enter limit answers 400 without reaching the maintenance service.
func TestEnterMaintenanceRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateNormal)
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(
		t, handler, http.MethodPost, "/api/server/maintenance/enter", strings.Repeat(`{"force":true,`, 1024),
	)
	if status != http.StatusBadRequest {
		t.Fatalf("POST enter with an oversized body answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}

	var doc struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode 400 of POST enter: %v (%s)", err, body)
	}

	if doc.Code != http.StatusBadRequest || doc.Message == "" {
		t.Fatalf("POST enter error = %+v, want code 400 with a message", doc)
	}

	if len(fake.forces) != 0 {
		t.Fatalf("enter force flags = %v, want no enter on an oversized body", fake.forces)
	}
}

func TestLeaveMaintenanceAnswersNormal(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateMaintenance)
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(t, handler, http.MethodPost, "/api/server/maintenance/leave", "")
	if status != http.StatusOK {
		t.Fatalf("POST leave answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc maintenanceState

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode leave %q: %v", body, err)
	}

	if doc.State != "normal" {
		t.Fatalf("leave state = %q, want normal", doc.State)
	}

	if fake.state != maintenance.StateNormal {
		t.Fatalf("fake state = %q, want normal after leave", fake.state)
	}
}

// TestMaintenanceUnavailableAnswersWithoutDriverText proves a failed state
// read answers 500 with the fixed unavailable message, never driver text.
func TestMaintenanceUnavailableAnswersWithoutDriverText(t *testing.T) {
	t.Parallel()

	fake := newFakeMaintenance(maintenance.StateNormal)
	fake.getErr = fmt.Errorf("select installation state sqlite database: %w", maintenance.ErrUnavailable)
	handler := newMaintenanceHandler(t, fake)

	status, body := serveMaintenance(t, handler, http.MethodGet, "/api/server/maintenance", "")
	if status != http.StatusInternalServerError {
		t.Fatalf("GET maintenance on a failed read answers %d, want %d (%s)",
			status, http.StatusInternalServerError, body)
	}

	var doc struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode 500 of GET maintenance: %v (%s)", err, body)
	}

	if doc.Code != http.StatusInternalServerError || doc.Message != maintenance.ErrUnavailable.Error() {
		t.Fatalf("GET maintenance error = %+v, want code 500 with the unavailable message", doc)
	}

	for _, text := range []string{"sqlite", "select", "driver"} {
		if strings.Contains(body, text) {
			t.Fatalf("GET maintenance error %q holds implementation text %q", body, text)
		}
	}
}

// TestNewHandlerRequiresMaintenance proves NewHandler fails without the
// maintenance service.
func TestNewHandlerRequiresMaintenance(t *testing.T) {
	t.Parallel()

	_, err := apiserver.NewHandler(apiserver.Config{
		Preferences:      nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       newTestNamespaces(t),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      nil,
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            &testAudit{},
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
	})
	if err == nil {
		t.Fatal("NewHandler without maintenance succeeded")
	}
}
