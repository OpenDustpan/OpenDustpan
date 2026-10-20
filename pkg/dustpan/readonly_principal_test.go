package dustpan_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	inventoryapi "opendustpan.org/opendustpan/internal/api/inventory"
	contextapi "opendustpan.org/opendustpan/internal/api/objectcontext"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
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
// namespace can read the unit list and the context of a unit, and get 403 on
// each route that changes data. The test stores both principals with their
// kind; the server has no trust-rule token exchange yet, so it does not sign
// a workload in through one.
func TestReadOnlyPrincipalReadsAndCannotWrite(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)

	// The tags of a stack are its labels in the unit list.
	code, body := w.call(t, http.MethodPatch, "/api/stacks/acme/web/prod/tags", ctxAdminToken, `{"team":"platform"}`)
	if code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("set stack tags: status = %d: %s", code, body)
	}

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
		{"deleted unit list", "/api/dustpan/v1/namespaces/acme/deleted-units"},
		{
			"unit context",
			"/api/dustpan/v1/namespaces/acme/units/" + w.stack + "/context?include=identity,health,changes",
		},
		{"workspace context", "/api/dustpan/v1/namespaces/acme/units/" + w.workspace + "/context"},
		{"namespace context", "/api/dustpan/v1/namespaces/acme/context"},
	}

	for _, tc := range reads {
		code, body := w.call(t, http.MethodGet, tc.target, token, "")
		if code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200: %s", tc.name, code, body)
		}
	}

	w.expectUnitIDs(t, token, "", []string{w.stack, w.workspace})
	w.expectUnitIDs(t, token, "?label=team%3Dplatform", []string{w.stack})
	w.expectUnitIDs(t, token, "?label=team%3Dnobody", nil)
}

// expectUnitIDs checks that the unit list of acme, with the query string
// query, holds exactly the units with the IDs want, in the order of the unit ID.
func (w *contextWorld) expectUnitIDs(t *testing.T, token, query string, want []string) {
	t.Helper()

	code, body := w.call(t, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units"+query, token, "")
	if code != http.StatusOK {
		t.Fatalf("unit list %q: status = %d: %s", query, code, body)
	}

	var list unitListWire

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode unit list %s: %v", body, err)
	}

	got := make([]string, 0, len(list.Units))
	for _, u := range list.Units {
		got = append(got, u.UnitID)
	}

	slices.Sort(want)

	if !slices.Equal(got, want) && (len(got) != 0 || len(want) != 0) {
		t.Fatalf("unit list %q holds %v, want %v (%s)", query, got, want, body)
	}
}

// expectReadOnlyWrites checks that token gets 403 on each route of the
// inventory and context handlers that changes data. It takes the routes from
// the route tables of both handlers, so a new write route is in the test with
// no edit.
func (w *contextWorld) expectReadOnlyWrites(t *testing.T, token string) {
	t.Helper()

	targets := writeTargets(t, w)
	if len(targets) < 9 {
		t.Fatalf(
			"write routes = %d, want at least the collection, restore, purge, move and context routes",
			len(targets),
		)
	}

	for _, tc := range targets {
		code, body := w.call(t, tc.method, tc.target, token, tc.body)
		if code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403: %s", tc.method, tc.pattern, code, body)
		}
	}
}

// writeTarget is one request to a route that changes data.
type writeTarget struct {
	pattern string
	method  string
	target  string
	body    string
}

// writeTargets lists a request for each route of the inventory and context
// route tables whose method is not GET. It fills the path values with the
// units of w.
func writeTargets(t *testing.T, w *contextWorld) []writeTarget {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: region})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	collections, err := collection.NewService(collection.Config{Store: st, Namespaces: st, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	writer, err := objectcontext.NewWriter(objectcontext.WriterConfig{Store: st, Namespaces: st, Now: nil})
	if err != nil {
		t.Fatalf("context writer: %v", err)
	}

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)

	units, err := inventoryapi.NewHandler(inventoryapi.Config{
		Audit: nil, OnAuditFailure: nil, Locks:     nil, PublicURL: "", Moves: nil, Namespaces: namespaces, Units: st, Collections: collections, Deleted: st,
		History: nil, Lifecycles: map[string]coreinventory.Lifecycle{"pulumi": nil}, ReadScope: allReadable{},
		Authenticator: authn, Authorizer: identity.AdminOnly{}, Sessions: nil, Logger: logger, Now: nil,
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	contexts, err := contextapi.NewHandler(contextapi.Config{
		Audit: nil, OnAuditFailure: nil,
		Namespaces: namespaces, Units: st, Deleted: st, Collections: collections, Entries: st, Writer: writer,
		Authenticator: authn, Authorizer: identity.AdminOnly{}, Sessions: nil, Logger: logger,
	})
	if err != nil {
		t.Fatalf("context handler: %v", err)
	}

	var out []writeTarget

	for _, route := range units.Routes() {
		out = appendWrite(out, route.Method, route.Path, w)
	}

	for _, route := range contexts.Routes() {
		out = appendWrite(out, route.Method, route.Path, w)
	}

	return out
}

// appendWrite adds a request for the route method and path to out when the
// method changes data.
func appendWrite(out []writeTarget, method, path string, w *contextWorld) []writeTarget {
	if method == http.MethodGet {
		return out
	}

	target := strings.NewReplacer(
		"{namespace}", "acme", "{unit_id}", w.stack, "{unitID}", w.stack, "{tool}", "pulumi", "{collection}", "core",
	).Replace(path)

	switch {
	case strings.Contains(target, "/deleted-units/"):
		target += "?organization=acme"
	case strings.HasSuffix(target, "/unit-collection"):
		target += "?unit=pulumi:acme/web/prod"
	default:
	}

	return append(out, writeTarget{pattern: method + " " + path, method: method, target: target, body: "{}"})
}

// allReadable is a read scope that lets every caller read every deleted unit.
// The route table does not call it.
type allReadable struct{}

func (allReadable) ReadableUnits(context.Context, identity.Principal, namespace.Name) (bool, []string, error) {
	return true, nil, nil
}
