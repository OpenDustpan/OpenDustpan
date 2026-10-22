package inventory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	unitByIDPath    = "/api/dustpan/v1/namespaces/-/units/"
	acmeUnitID      = "stk-acme"
	globexUnitID    = "stk-globex"
	deletedUnitByID = "stk-gone"
)

// grantAuthorizer allows exactly the pairs of an action and an object wire
// form in allow, and denies everything else.
type grantAuthorizer struct {
	allow map[string]bool
}

func grant(action identity.Action, object string) string {
	return string(action) + "|" + object
}

func (g grantAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if g.allow[grant(action, obj.String())] {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (grantAuthorizer) ListObjects(
	_ context.Context, p identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// unitEntries holds one live unit in acme and one in globex. The deleted unit
// is in the deleted reader only.
func unitEntries() map[namespace.Name][]coreinventory.Entry {
	count := int64(7)

	return map[namespace.Name][]coreinventory.Entry{
		"acme": {{
			ManualLock:    nil,
			LastChanged:   changeTime(),
			Namespace:     "acme",
			Tool:          "pulumi",
			Name:          "web/prod",
			UnitID:        acmeUnitID,
			DisplayName:   "web/prod",
			Status:        "succeeded",
			ResourceCount: &count,
			Lock: &coreinventory.Lock{
				Since: changeTime().Add(time.Minute), Holder: "alice", Operation: "update",
			},
			Collection: "",
			Labels:     map[string]string{"team": "payments"},
		}},
		"globex": {{
			ManualLock:    nil,
			LastChanged:   changeTime(),
			Namespace:     "globex",
			Tool:          "opentofu",
			Name:          "net",
			UnitID:        globexUnitID,
			DisplayName:   "net",
			Status:        "",
			ResourceCount: nil,
			Lock:          nil,
			Collection:    "",
			Labels:        map[string]string{},
		}},
	}
}

func newUnitHandler(t *testing.T, authz identity.Authorizer) *apiinventory.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	deleted := &memDeleted{entries: []coreinventory.DeletedEntry{
		deletedEntry("pulumi", "old/gone", deletedUnitByID, 30),
	}}

	handler, err := apiinventory.NewHandler(apiinventory.Config{
		Views: nil,
		Audit: nil, OnAuditFailure: nil, Locks: nil, Moves: nil,
		Collections:   testCollectionService(t, homedNamespaceStore{}),
		Namespaces:    namespaces,
		Units:         &memReader{entries: unitEntries(), err: nil},
		Deleted:       deleted,
		History:       nil,
		Lifecycles:    testLifecycles(),
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		Now:           nil,
		PublicURL:     "https://dustpan.example.com",
		DenialLimiter: nil,
		StateGuard:    nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

func getUnitByID(t *testing.T, h http.Handler, id, token string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, unitByIDPath+id, http.NoBody)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}

	return out
}

const notFoundBody = `{"message":"unit not found","code":404,"errorCode":"unit.not-found"}` + "\n"

// TestGetUnitByID is table-driven over the callers and the units of the route.
func TestGetUnitByID(t *testing.T) {
	t.Parallel()

	// acmeOnly may read the unit in globex but not the namespace globex.
	acmeOnly := grantAuthorizer{allow: map[string]bool{
		grant(identity.ActionNamespaceRead, "acme"):            true,
		grant(identity.ActionUnitRead, "pulumi:acme/web/prod"): true,
		grant(identity.ActionUnitRead, "opentofu:globex/net"):  true,
	}}
	everything := grantAuthorizer{allow: map[string]bool{
		grant(identity.ActionNamespaceRead, "acme"):            true,
		grant(identity.ActionNamespaceRead, "globex"):          true,
		grant(identity.ActionUnitRead, "pulumi:acme/web/prod"): true,
		grant(identity.ActionUnitRead, "opentofu:globex/net"):  true,
		grant(identity.ActionUnitRead, "pulumi:acme/old/gone"): true,
	}}
	noUnitRead := grantAuthorizer{allow: map[string]bool{
		grant(identity.ActionNamespaceRead, "acme"): true,
	}}

	type testCase struct {
		name       string
		authz      identity.Authorizer
		id         string
		token      string
		wantStatus int
		wantBody   string
		wantNS     string
		wantTool   string
	}

	found := func(name, id, ns, tool string) testCase {
		return testCase{
			name: name, authz: everything, id: id, token: "test-token",
			wantStatus: http.StatusOK, wantBody: "", wantNS: ns, wantTool: tool,
		}
	}
	missing := func(name string, authz identity.Authorizer, id string) testCase {
		return testCase{
			name: name, authz: authz, id: id, token: "test-token",
			wantStatus: http.StatusNotFound, wantBody: notFoundBody, wantNS: "", wantTool: "",
		}
	}

	tests := []testCase{
		found("found", acmeUnitID, "acme", "Pulumi"),
		found("found in another namespace", globexUnitID, "globex", "OpenTofu"),
		missing("unknown unit", everything, "stk-unknown"),
		missing("unit the caller may not read", noUnitRead, acmeUnitID),
		missing("namespace the caller may not read", acmeOnly, globexUnitID),
		missing("deleted unit", everything, deletedUnitByID),
		{
			name: "no token", authz: everything, id: acmeUnitID, token: "",
			wantStatus: http.StatusUnauthorized, wantBody: "", wantNS: "", wantTool: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := getUnitByID(t, newUnitHandler(t, tc.authz), tc.id, tc.token)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			if tc.wantBody != "" && rec.Body.String() != tc.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}

			if tc.wantStatus != http.StatusOK {
				return
			}

			got := decodeObject(t, rec)
			if got["namespace"] != tc.wantNS || got["tool"] != tc.wantTool || got["unitID"] != tc.id {
				t.Fatalf("unit = %v, want namespace %s tool %s unit %s", got, tc.wantNS, tc.wantTool, tc.id)
			}
		})
	}
}

// TestGetUnitByIDOldPath proves that the paths without the "-" namespace
// segment are not served.
func TestGetUnitByIDOldPath(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/api/dustpan/v1/units/" + acmeUnitID,
		"/api/dustpan/v1/namespaces/acme/units/" + acmeUnitID,
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			handler := newUnitHandler(t, grantAuthorizer{allow: nil})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
			req.Header.Set("Authorization", "token test-token")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusNotFound, rec.Body.String())
			}
		})
	}
}

// TestListUnitsDashNamespace proves that the list route refuses "-" as a
// namespace name.
func TestListUnitsDashNamespace(t *testing.T) {
	t.Parallel()

	handler := newUnitHandler(t, grantAuthorizer{allow: nil})

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/-/units", http.NoBody,
	)
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestGetUnitByIDMatchesList proves the route answers the object that the
// namespace list gives for the same unit.
func TestGetUnitByIDMatchesList(t *testing.T) {
	t.Parallel()

	allow := grantAuthorizer{allow: map[string]bool{
		grant(identity.ActionNamespaceRead, "acme"):            true,
		grant(identity.ActionUnitRead, "pulumi:acme/web/prod"): true,
	}}
	handler := newUnitHandler(t, allow)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", http.NoBody,
	)
	req.SetPathValue("namespace", "acme")
	req.Header.Set("Authorization", "token test-token")

	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, req)

	var list struct {
		Units []map[string]any `json:"units"`
	}

	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil || len(list.Units) != 1 {
		t.Fatalf("list = %q, err %v: want one unit", listRec.Body.String(), err)
	}

	got := decodeObject(t, getUnitByID(t, handler, acmeUnitID, "test-token"))
	if !reflect.DeepEqual(got, list.Units[0]) {
		t.Fatalf("by ID = %v, list item = %v: want equal", got, list.Units[0])
	}

	want := map[string]any{
		"consoleUrl": "https://dustpan.example.com/units/" + acmeUnitID,
		"lastChange": changeTime().Format(time.RFC3339Nano),
		"labels":     map[string]any{"team": "payments"},
	}
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			t.Fatalf("%s = %v, want %v", key, got[key], value)
		}
	}

	lock, ok := got["lock"].(map[string]any)
	if !ok || lock["holder"] != "alice" || lock["operation"] != "update" {
		t.Fatalf("lock = %v, want holder alice and operation update", got["lock"])
	}
}

// TestGetUnitByIDRouteRow proves the row declares both actions and the 404
// on denial, so the route test and the policy check see them.
func TestGetUnitByIDRouteRow(t *testing.T) {
	t.Parallel()

	var found bool

	for _, rt := range newUnitHandler(t, grantAuthorizer{allow: nil}).Routes() {
		if rt.Path != "/api/dustpan/v1/namespaces/-/units/{unitID}" {
			continue
		}

		found = true

		if rt.Method != http.MethodGet || rt.Scope != route.ScopeAuthorized ||
			!reflect.DeepEqual(rt.Actions, []identity.Action{identity.ActionUnitRead}) ||
			!reflect.DeepEqual(rt.NamespaceActions, []identity.Action{identity.ActionNamespaceRead}) ||
			!rt.DeniedAsNotFound {
			t.Fatalf("route row = %+v", rt)
		}
	}

	if !found {
		t.Fatal("route table has no unit-by-ID row")
	}
}
