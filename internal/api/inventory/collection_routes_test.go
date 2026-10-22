package inventory_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// isCollectionRoute reports whether a route belongs to the collection rows. Those
// rows fill their path values and answer their own statuses, so TestCollectionRoutes
// checks them instead of the generic route tests. The saved view rows need a
// person, a body and a view ID, so TestViewActions checks them.
func isCollectionRoute(route apiinventory.Route) bool {
	return strings.Contains(route.Path, "/collections") || strings.Contains(route.Path, "unit-collection") ||
		strings.Contains(route.Path, "/views")
}

// newCollectionRouteHandler returns a handler with the collections of the acme
// namespace: "old" holds the unit pulumi:acme/web/prod, and "payments" is empty.
func newCollectionRouteHandler(t *testing.T, authz identity.Authorizer) testHandler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	seedNamespace(t, nsStore)

	store := newMemCollections()
	unit := collection.UnitRef{Namespace: "acme", Tool: "pulumi", Name: "web/prod"}
	store.units[unit] = true
	store.members[unit] = "old"

	for _, name := range []string{"old", "payments"} {
		store.collections[collectionKey("acme", collection.Name(name))] = collection.Name(name)
	}

	svc, err := collection.NewService(collection.Config{Store: store, Namespaces: nsStore, Now: nil})
	if err != nil {
		t.Fatalf("collection service: %v", err)
	}

	nsSvc, err := namespace.NewService(namespace.Config{Store: nsStore, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, apiinventory.Config{
		Views: nil,
		Audit: newTestAudit(), OnAuditFailure: nil, Locks: nil, PublicURL: "",
		Namespaces:    nsSvc,
		Units:         &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil},
		Collections:   svc,
		Moves:         allowMoves{},
		Deleted:       &memDeleted{entries: nil},
		History:       nil,
		Lifecycles:    testLifecycles(),
		ReadScope:     fixedScope{err: nil, ids: nil, all: true},
		Now:           nil,
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
		DenialLimiter: nil,
		StateGuard:    nil,
	})
	if err != nil {
		t.Fatalf("inventory handler: %v", err)
	}

	return handler
}

// collectionRouteRequest fills the {namespace} and {collection} path values of a
// route, and the unit query of the move route, and signs the request in.
func collectionRouteRequest(t *testing.T, route apiinventory.Route, body string) *http.Request {
	t.Helper()

	target := strings.NewReplacer("{namespace}", "acme", "{collection}", "payments").Replace(route.Path)
	if strings.HasSuffix(route.Path, "unit-collection") {
		target += "?unit=pulumi:acme/web/prod"
	}

	req := httptest.NewRequestWithContext(t.Context(), route.Method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "token test-token")

	return req
}

// collectionRouteCase is the answer that each collection route must give to a
// signed-in admin with the path values filled.
type collectionRouteCase struct {
	body   string
	status int
}

// collectionRouteCases maps the method and path of each collection route to its
// request body and its allowed status. A route missing here fails the test.
var collectionRouteCases = map[string]collectionRouteCase{ //nolint:gochecknoglobals // A read-only table of expected answers.
	http.MethodGet + " /api/dustpan/v1/namespaces/{namespace}/collections": {
		body:   "",
		status: http.StatusOK,
	},
	http.MethodPost + " /api/dustpan/v1/namespaces/{namespace}/collections": {
		body:   `{"name":"platform"}`,
		status: http.StatusCreated,
	},
	http.MethodGet + " /api/dustpan/v1/namespaces/{namespace}/collections/{collection}": {
		body:   "",
		status: http.StatusOK,
	},
	http.MethodDelete + " /api/dustpan/v1/namespaces/{namespace}/collections/{collection}": {
		body:   "",
		status: http.StatusNoContent,
	},
	http.MethodPut + " /api/inventory/unit-collection": {
		body:   `{"collection":"payments"}`,
		status: http.StatusOK,
	},
}

// TestCollectionRoutes proves each collection route is served with its path
// values filled. It answers its allowed status to an admin, and 403 when its
// declared action is denied. Each allowed write runs against the seeded store.
func TestCollectionRoutes(t *testing.T) {
	t.Parallel()

	handler := newCollectionRouteHandler(t, selectiveAuthorizer{deny: nil})

	seen := 0

	for _, route := range handler.Routes() {
		if !isCollectionRoute(route) {
			continue
		}

		seen++

		key := route.Method + " " + route.Path

		want, ok := collectionRouteCases[key]
		if !ok {
			t.Fatalf("collection route %q has no expected answer", key)
		}

		t.Run(key, func(t *testing.T) {
			t.Parallel()

			allowed := newCollectionRouteHandler(t, selectiveAuthorizer{deny: nil})

			rec := httptest.NewRecorder()
			allowed.ServeHTTP(rec, collectionRouteRequest(t, route, want.body))

			if rec.Code != want.status {
				t.Fatalf("allowed request answers %d, want %d: %s", rec.Code, want.status, rec.Body.String())
			}

			deny := map[identity.Action]bool{}
			for _, action := range route.Actions {
				deny[action] = true
			}

			denied := newCollectionRouteHandler(t, selectiveAuthorizer{deny: deny})

			rec = httptest.NewRecorder()
			denied.ServeHTTP(rec, collectionRouteRequest(t, route, want.body))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("denied request answers %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}

	if seen != len(collectionRouteCases) {
		t.Fatalf("handler serves %d collection routes, the table names %d", seen, len(collectionRouteCases))
	}
}
