package inventory_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// outputSharingPath is the setting route of the namespace that the tests use.
const outputSharingPath = "/api/dustpan/v1/namespaces/acme/output-sharing"

// memOutputSharing is an outputshare.Settings in memory over the one
// namespace acme. It accepts any value and records each change with its audit
// entry, so the tests prove the checks of the handler and of the service.
type memOutputSharing struct {
	values  map[namespace.Name]outputshare.Default
	entries []audit.Entry
	mu      sync.Mutex
}

func newMemOutputSharing() *memOutputSharing {
	return &memOutputSharing{values: map[namespace.Name]outputshare.Default{}, entries: nil, mu: sync.Mutex{}}
}

func (m *memOutputSharing) Setting(_ context.Context, ns namespace.Name) (outputshare.Default, error) {
	if ns != "acme" {
		return "", namespace.ErrNotFound
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if d, ok := m.values[ns]; ok {
		return d, nil
	}

	return outputshare.DefaultNobody, nil
}

func (m *memOutputSharing) SetSetting(
	_ context.Context, ns namespace.Name, d outputshare.Default, _ namespace.Fence, e audit.Entry,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.values[ns] = d
	m.entries = append(m.entries, e)

	return nil
}

// value returns the stored setting of acme, or "" when it was never set.
func (m *memOutputSharing) value() outputshare.Default {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.values["acme"]
}

// changes returns the number of the stored changes.
func (m *memOutputSharing) changes() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.entries)
}

// testOutputSharing returns a service of the setting over settings, with the
// namespaces of nsStore and the authorizer authz.
func testOutputSharing(
	t *testing.T, settings outputshare.Settings, nsStore namespace.Store, authz outputsharesvc.Authorizer,
) *outputsharesvc.Service {
	t.Helper()

	svc, err := outputsharesvc.New(outputsharesvc.Config{
		Settings: settings, Namespaces: nsStore, Authorizer: authz, Now: nil,
	})
	if err != nil {
		t.Fatalf("output sharing service: %v", err)
	}

	return svc
}

// sharingFixture is a handler with the setting routes over an in-memory
// store.
type sharingFixture struct {
	lockFixture

	settings *memOutputSharing
}

// newSharingFixture returns a handler whose routes ask routeAuthz, and whose
// service asks serviceAuthz.
func newSharingFixture(t *testing.T, routeAuthz, serviceAuthz identity.Authorizer) sharingFixture {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	nsSvc, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	settings := newMemOutputSharing()
	auditStore := &memAudit{mu: sync.Mutex{}, entries: nil}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		OutputSharing:  testOutputSharing(t, settings, nsStore, serviceAuthz),
		Outputs:        nil,
		Views:          nil,
		Namespaces:     nsSvc,
		Units:          &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Deleted:        &memDeleted{entries: nil},
		History:        nil,
		Lifecycles:     testLifecycles(),
		ReadScope:      fixedScope{err: nil, ids: nil, all: true},
		Collections:    nil,
		Locks:          nil,
		Moves:          nil,
		Authenticator:  subjectAuthenticator{},
		Authorizer:     routeAuthz,
		Audit:          orTestAudit(auditStore),
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		PublicURL:      "",
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
		StateGuard:     nil,
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	return sharingFixture{handler: handler, store: nil, audit: auditStore, settings: settings}
}

// TestOutputSharingRoutesDeclareActions proves the rows of the setting: a read
// needs namespace:read and a change needs unit:manage-sharing, each on the
// namespace of the path.
func TestOutputSharingRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	f := newSharingFixture(t, selectiveAuthorizer{deny: nil}, selectiveAuthorizer{deny: nil})

	want := map[string]identity.Action{
		"GET /api/dustpan/v1/namespaces/{namespace}/output-sharing": identity.ActionNamespaceRead,
		"PUT /api/dustpan/v1/namespaces/{namespace}/output-sharing": identity.ActionUnitManageSharing,
	}

	found := 0

	for _, route := range f.handler.Routes() {
		action, ok := want[route.Method+" "+route.Path]
		if !ok {
			continue
		}

		found++

		checkRouteScope(t, route, map[string]bool{})

		if len(route.Actions) != 1 || route.Actions[0] != action || len(route.NamespaceActions) != 0 {
			t.Errorf("route %s %s actions = %v, want [%s]", route.Method, route.Path, route.Actions, action)
		}

		obj, err := route.Resolve(nil, routeRequest(t, route, "test-token"))
		if err != nil || obj.String() != "acme" || !obj.IsNamespace() {
			t.Errorf(
				"route %s %s object = %q, %v, want the namespace acme",
				route.Method,
				route.Path,
				obj.String(),
				err,
			)
		}

		probeRoute(t, f.handler, route)
	}

	if found != len(want) {
		t.Fatalf("the handler serves %d setting routes, want %d", found, len(want))
	}
}

// TestNoOutputSharingRoutesWithoutTheService proves that a handler built
// without the service serves no setting route.
func TestNoOutputSharingRoutesWithoutTheService(t *testing.T) {
	t.Parallel()

	f := newLockFixture(t, selectiveAuthorizer{deny: nil})

	for _, route := range f.handler.Routes() {
		if strings.HasSuffix(route.Path, "/output-sharing") {
			t.Errorf("route %s %s is served without the service", route.Method, route.Path)
		}
	}
}

// TestOutputSharingRoundTrip proves that a new namespace reads nobody, that a
// change answers the stored value and writes one audit entry of the caller,
// and that the read then answers the new value.
func TestOutputSharingRoundTrip(t *testing.T) {
	t.Parallel()

	f := newSharingFixture(t, selectiveAuthorizer{deny: nil}, selectiveAuthorizer{deny: nil})

	rec := f.do(t, "alice", http.MethodGet, outputSharingPath, "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"newUnitDefault":"nobody"}` {
		t.Fatalf("read = %d %s, want 200 with nobody", rec.Code, rec.Body.String())
	}

	rec = f.do(t, "alice", http.MethodPut, outputSharingPath, `{"newUnitDefault":"namespace"}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"newUnitDefault":"namespace"}` {
		t.Fatalf("change = %d %s, want 200 with namespace", rec.Code, rec.Body.String())
	}

	rec = f.do(t, "alice", http.MethodGet, outputSharingPath, "")
	if strings.TrimSpace(rec.Body.String()) != `{"newUnitDefault":"namespace"}` {
		t.Fatalf("read after the change = %s, want namespace", rec.Body.String())
	}

	checkSettingEntry(t, f.settings.entries)
}

// checkSettingEntry fails the test unless entries is the one audit entry of
// the change of acme by alice to namespace.
func checkSettingEntry(t *testing.T, entries []audit.Entry) {
	t.Helper()

	if len(entries) != 1 {
		t.Fatalf("the change wrote %d audit entries, want 1", len(entries))
	}

	e := entries[0]
	if e.Subject != "alice" || e.Namespace != "acme" || e.Object != "acme" || e.Action != "unit:manage-sharing" ||
		len(e.Changes) != 1 || e.Changes[0].New != "namespace" {
		t.Fatalf("audit entry = %+v, want the change of acme by alice", e)
	}
}

// TestOutputSharingRefusesABadBody proves the allowlist of the boundary alone:
// the store and the service behind it accept each body here or never see it,
// and each body answers 400 and changes nothing.
func TestOutputSharingRefusesABadBody(t *testing.T) {
	t.Parallel()

	bodies := []string{
		"",
		"null",
		"[]",
		"{}",
		`{"newUnitDefault":null}`,
		`{"newUnitDefault":""}`,
		`{"newUnitDefault":"everyone"}`,
		`{"newUnitDefault":"Nobody"}`,
		`{"newUnitDefault":"namespace "}`,
		`{"newUnitDefault":"namespace\n"}`,
		`{"newUnitDefault":"namespace\r\nX-Injected: 1"}`,
		`{"newUnitDefault":1}`,
		`{"newUnitDefault":"nobody","rules":[]}`,
		`{"newUnitDefault":"nobody"}{"newUnitDefault":"namespace"}`,
		`{"newUnitDefault":"` + strings.Repeat("n", 2048) + `"}`,
	}

	for _, body := range bodies {
		f := newSharingFixture(t, selectiveAuthorizer{deny: nil}, selectiveAuthorizer{deny: nil})

		rec := f.do(t, "alice", http.MethodPut, outputSharingPath, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q answers %d, want 400: %s", body, rec.Code, rec.Body.String())
		}

		if strings.Contains(rec.Body.String(), "X-Injected") {
			t.Fatalf("the answer to body %q echoes the value: %s", body, rec.Body.String())
		}

		if f.settings.changes() != 0 || f.settings.value() != "" {
			t.Fatalf("body %q changed the setting to %q", body, f.settings.value())
		}
	}
}

// TestOutputSharingRoutesNeedTheirActions proves that the route check alone
// refuses a caller without the action of the route with 403, writes one
// denied entry, and changes nothing. A holder of unit:read-outputs or of
// namespace:read alone cannot change the setting.
func TestOutputSharingRoutesNeedTheirActions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		deny   identity.Action
		method string
		body   string
	}{
		{identity.ActionNamespaceRead, http.MethodGet, ""},
		{identity.ActionUnitManageSharing, http.MethodPut, `{"newUnitDefault":"namespace"}`},
	}

	for _, tc := range cases {
		f := newSharingFixture(t,
			selectiveAuthorizer{deny: map[identity.Action]bool{tc.deny: true}}, selectiveAuthorizer{deny: nil})

		rec := f.do(t, "alice", tc.method, outputSharingPath, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s with %s denied answers %d, want 403: %s", tc.method, tc.deny, rec.Code, rec.Body.String())
		}

		if f.settings.changes() != 0 {
			t.Fatalf("%s with %s denied changed the setting", tc.method, tc.deny)
		}

		got := f.audit.denied()
		if len(got) != 1 || got[0].Action != string(tc.deny) || got[0].Subject != "alice" {
			t.Fatalf("denied entries = %+v, want one of %s by alice", got, tc.deny)
		}
	}
}

// TestOutputSharingServiceRefusal proves the check of the service alone: the
// route check allows the caller, the service denies, and the handler answers
// 403, writes one denied entry and changes nothing.
func TestOutputSharingServiceRefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		deny   identity.Action
		method string
		body   string
	}{
		{identity.ActionNamespaceRead, http.MethodGet, ""},
		{identity.ActionUnitManageSharing, http.MethodPut, `{"newUnitDefault":"namespace"}`},
	}

	for _, tc := range cases {
		f := newSharingFixture(t,
			selectiveAuthorizer{deny: nil}, selectiveAuthorizer{deny: map[identity.Action]bool{tc.deny: true}})

		rec := f.do(t, "alice", tc.method, outputSharingPath, tc.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s with %s denied by the service answers %d, want 403", tc.method, tc.deny, rec.Code)
		}

		if f.settings.changes() != 0 {
			t.Fatalf("%s with %s denied by the service changed the setting", tc.method, tc.deny)
		}

		got := f.audit.denied()
		if len(got) != 1 || got[0].Action != string(tc.deny) {
			t.Fatalf("denied entries = %+v, want one of %s", got, tc.deny)
		}
	}
}

// TestOutputSharingOfAnUnknownOrBadNamespace proves that a namespace that does
// not exist answers 404, and that a name that no namespace can have answers
// 400, and that neither changes a setting.
func TestOutputSharingOfAnUnknownOrBadNamespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		want int
	}{
		{"/api/dustpan/v1/namespaces/absent/output-sharing", http.StatusNotFound},
		{"/api/dustpan/v1/namespaces/.acme/output-sharing", http.StatusBadRequest},
		{"/api/dustpan/v1/namespaces/ac%20me/output-sharing", http.StatusBadRequest},
	}

	for _, tc := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			f := newSharingFixture(t, selectiveAuthorizer{deny: nil}, selectiveAuthorizer{deny: nil})

			rec := f.do(t, "alice", method, tc.path, `{"newUnitDefault":"namespace"}`)
			if rec.Code != tc.want {
				t.Fatalf("%s %s answers %d, want %d: %s", method, tc.path, rec.Code, tc.want, rec.Body.String())
			}

			if f.settings.changes() != 0 {
				t.Fatalf("%s %s changed a setting", method, tc.path)
			}
		}
	}
}

// brokenOutputSharing is an outputshare.Settings whose stored value fails
// validation, as a row of another version could.
type brokenOutputSharing struct{}

func (brokenOutputSharing) Setting(_ context.Context, _ namespace.Name) (outputshare.Default, error) {
	return "", fmt.Errorf("%w: the stored output sharing setting", outputshare.ErrInvalid)
}

func (brokenOutputSharing) SetSetting(
	_ context.Context, _ namespace.Name, _ outputshare.Default, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

// TestOutputSharingFailsClosedOnABadStoredValue proves that a stored value
// that fails validation answers 500 with no value, and never a default, on
// the read.
func TestOutputSharingFailsClosedOnABadStoredValue(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	f := newSharingFixture(t, selectiveAuthorizer{deny: nil}, selectiveAuthorizer{deny: nil})
	broken := testOutputSharing(t, brokenOutputSharing{}, nsStore, selectiveAuthorizer{deny: nil})

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		OutputSharing:  broken,
		Outputs:        nil,
		Views:          nil,
		Namespaces:     mustNamespaces(t, nsStore),
		Units:          &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Deleted:        &memDeleted{entries: nil},
		History:        nil,
		Lifecycles:     testLifecycles(),
		ReadScope:      fixedScope{err: nil, ids: nil, all: true},
		Collections:    nil,
		Locks:          nil,
		Moves:          nil,
		Authenticator:  subjectAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: nil},
		Audit:          orTestAudit(f.audit),
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		PublicURL:      "",
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
		StateGuard:     nil,
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	f.handler = handler

	rec := f.do(t, "alice", http.MethodGet, outputSharingPath, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET answers %d, want 500: %s", rec.Code, rec.Body.String())
	}

	if strings.Contains(rec.Body.String(), "newUnitDefault") || strings.Contains(rec.Body.String(), "nobody") {
		t.Fatalf("GET answers a value: %s", rec.Body.String())
	}
}

// mustNamespaces returns the namespace service over nsStore.
func mustNamespaces(t *testing.T, nsStore namespace.Store) *namespace.Service {
	t.Helper()

	svc, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	return svc
}
