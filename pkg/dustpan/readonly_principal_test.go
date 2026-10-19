package dustpan_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// seedReadOnlyPrincipals stores the role that holds namespace:read and
// unit:read only, a service principal and a workload principal, and grants
// the role to both on acme.
func seedReadOnlyPrincipals(t *testing.T, st *sqlite.Store) {
	t.Helper()

	at := time.Unix(0, 0)
	readOnly := access.Role{
		CreatedAt: at, UpdatedAt: at, ID: ctxReadOnlyRole, Name: "read only", Description: "", BuiltIn: false,
		Actions: []identity.Action{identity.ActionNamespaceRead, identity.ActionUnitRead},
	}

	if err := st.CreateRole(t.Context(), readOnly, storetest.ServerChange(at, "role:"+ctxReadOnlyRole)); err != nil {
		t.Fatalf("create role: %v", err)
	}

	storePrincipalOfKind(t, st, "svc-reader", principal.KindService)
	storeGrant(t, st, "svc-reader", ctxReadOnlyRole, identity.NamespaceObject("acme"))
	storePrincipalOfKind(t, st, "wl-reader", principal.KindWorkload)
	storeGrant(t, st, "wl-reader", ctxReadOnlyRole, identity.NamespaceObject("acme"))
}

// unitListWire is the part of the unit list that the test reads.
type unitListWire struct {
	Units []unitIDWire `json:"units"`
}

// unitIDWire is the unit ID of an entry of the unit list.
type unitIDWire struct {
	UnitID string `json:"unitID"`
}

// TestReadOnlyPrincipalReadsAndCannotWrite shows that a service principal and
// a workload principal that hold only namespace:read and unit:read on a
// namespace can read the unit list and the context of a unit, on the paths
// under /api/dustpan/v1 and on the older paths, and get 403 on each route that
// changes data.
func TestReadOnlyPrincipalReadsAndCannotWrite(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)

	principals := []struct {
		name  string
		token string
	}{
		{name: "service principal", token: ctxServiceToken},
		{name: "workload principal", token: ctxWorkloadToken},
	}

	for _, who := range principals {
		t.Run(who.name, func(t *testing.T) {
			t.Parallel()

			w.expectReadOnlyReads(t, who.token)
			w.expectReadOnlyWrites(t, who.token)
		})
	}
}

// expectReadOnlyReads checks that token reads the list and the context.
func (w *contextWorld) expectReadOnlyReads(t *testing.T, token string) {
	t.Helper()

	reads := []struct {
		name   string
		target string
	}{
		{"unit list", "/api/dustpan/v1/namespaces/acme/units"},
		{"unit list by label", "/api/dustpan/v1/namespaces/acme/units?label=team%3Dplatform"},
		{"older unit list", "/api/inventory/units?organization=acme"},
		{"deleted unit list", "/api/dustpan/v1/namespaces/acme/deleted-units"},
		{
			"unit context",
			"/api/dustpan/v1/namespaces/acme/units/" + w.stack + "/context?include=identity,health,changes",
		},
		{"workspace context", "/api/dustpan/v1/namespaces/acme/units/" + w.workspace + "/context"},
		{"namespace context", "/api/dustpan/v1/namespaces/acme/context"},
		{"older unit context", "/api/context/namespaces/acme/units/pulumi/" + w.stack},
	}

	for _, tc := range reads {
		code, body := w.call(t, http.MethodGet, tc.target, token, "")
		if code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200: %s", tc.name, code, body)
		}
	}

	code, body := w.call(t, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", token, "")
	if code != http.StatusOK {
		t.Fatalf("unit list: status = %d: %s", code, body)
	}

	var list unitListWire

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode unit list %s: %v", body, err)
	}

	if len(list.Units) != 2 {
		t.Fatalf("units = %s, want the stack and the workspace of acme", body)
	}

	if !strings.Contains(body, w.stack) || !strings.Contains(body, w.workspace) {
		t.Fatalf("units = %s, want unit IDs %s and %s", body, w.stack, w.workspace)
	}
}

// expectReadOnlyWrites checks that token gets 403 on each write route.
func (w *contextWorld) expectReadOnlyWrites(t *testing.T, token string) {
	t.Helper()

	unitContext := "/api/dustpan/v1/namespaces/acme/units/" + w.stack + "/context"
	olderContext := "/api/context/namespaces/acme/units/pulumi/" + w.stack

	writes := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"create collection", http.MethodPost, "/api/dustpan/v1/namespaces/acme/collections", `{"name":"core"}`},
		{
			"create collection, older path",
			http.MethodPost,
			"/api/inventory/namespaces/acme/collections",
			`{"name":"core"}`,
		},
		{"delete collection", http.MethodDelete, "/api/dustpan/v1/namespaces/acme/collections/core", ""},
		{"delete collection, older path", http.MethodDelete, "/api/inventory/namespaces/acme/collections/core", ""},
		{
			"move unit",
			http.MethodPut,
			"/api/inventory/unit-collection?unit=pulumi:acme/web/prod",
			`{"collection":"core"}`,
		},
		{
			"restore unit",
			http.MethodPost,
			"/api/inventory/deleted-units/pulumi/" + w.stack + "/restore?organization=acme",
			`{}`,
		},
		{"purge unit", http.MethodDelete, "/api/inventory/deleted-units/pulumi/" + w.stack + "?organization=acme", ""},
		{"change unit context", http.MethodPatch, unitContext, `{"summary":"x"}`},
		{"clear unit context", http.MethodDelete, unitContext, ""},
		{"change unit context, older path", http.MethodPatch, olderContext, `{"summary":"x"}`},
		{"clear unit context, older path", http.MethodDelete, olderContext, ""},
		{"change namespace context", http.MethodPatch, "/api/dustpan/v1/namespaces/acme/context", `{"summary":"x"}`},
		{"clear namespace context", http.MethodDelete, "/api/dustpan/v1/namespaces/acme/context", ""},
		{"change namespace context, older path", http.MethodPatch, "/api/context/namespaces/acme", `{"summary":"x"}`},
		{"clear namespace context, older path", http.MethodDelete, "/api/context/namespaces/acme", ""},
	}

	for _, tc := range writes {
		code, body := w.call(t, tc.method, tc.target, token, tc.body)
		if code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403: %s", tc.name, code, body)
		}
	}
}
