package dustpan_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// tofuLockBody is the lock info that the OpenTofu http backend sends with
// LOCK.
const tofuLockBody = `{"ID":"probe","Operation":"OperationTypePlan","Who":"ci","Version":"1.13.2",` +
	`"Created":"2026-10-08T21:00:00Z","Path":""}`

// lockRoute finds a route in monospace after its method, as the lock info of
// a manual lock gives it.
var lockRoute = regexp.MustCompile("`(DELETE|POST) ([^`]+)`")

// TestTofuLockInfoNamesMountedRoutes proves that the routes in the lock info
// of a manual lock, which the OpenTofu http backend gives in its 423 answer,
// are routes that the server serves: for a unit, a collection and a namespace
// lock, the DELETE route removes the lock and the POST route force-unlocks it.
// The OpenTofu adapter writes the routes from its own text, so only a test on
// the whole server can see a change of the inventory routes.
func TestTofuLockInfoNamesMountedRoutes(t *testing.T) {
	t.Parallel()

	targets := []struct {
		name string
		lock string
	}{
		{name: "unit", lock: "/api/inventory/unit-lock?unit=opentofu:acme/web"},
		{name: "collection", lock: "/api/dustpan/v1/namespaces/acme/collections/payments/lock"},
		{name: "namespace", lock: "/api/dustpan/v1/namespaces/acme/lock"},
	}

	for _, tc := range targets {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.createNamespace(t, "acme", installationName)
			workspaceInCollection(t, f)

			routes := refusedRoutes(t, f, tc.lock)
			mustDo(t, f, call{method: http.MethodDelete, path: routes.remove, body: ""}, http.StatusNoContent)

			again := refusedRoutes(t, f, tc.lock)
			if again != routes {
				t.Fatalf("routes = %+v, then %+v", routes, again)
			}

			mustDo(t, f, call{method: http.MethodPost, path: routes.force, body: ""}, http.StatusNoContent)
		})
	}
}

// workspaceInCollection makes the workspace acme/web with a LOCK and an UNLOCK of
// the http backend, and puts it in the collection acme/payments.
func workspaceInCollection(t *testing.T, f fixture) {
	t.Helper()

	seedWebWorkspace(t, f)

	if code, body := tofuDo(t, f, "LOCK", tofuLockBody); code != http.StatusOK {
		t.Fatalf("LOCK = %d, want 200: %s", code, body)
	}

	if code, body := tofuDo(t, f, "UNLOCK", tofuLockBody); code != http.StatusOK {
		t.Fatalf("UNLOCK = %d, want 200: %s", code, body)
	}

	mustDo(t, f, call{
		method: http.MethodPost, path: "/api/dustpan/v1/namespaces/acme/collections", body: `{"name":"payments"}`,
	}, http.StatusCreated)
	mustDo(t, f, call{
		method: http.MethodPut, path: "/api/inventory/unit-collection?unit=opentofu:acme/web",
		body: `{"collection":"payments"}`,
	}, http.StatusOK)
}

// call is one request of the admin to the API.
type call struct {
	method string
	path   string
	body   string
}

// mustDo sends c with the admin token and fails the test when the status is
// not want.
func mustDo(t *testing.T, f fixture, c call, want int) {
	t.Helper()

	if code, got := f.do(t, c.method, c.path, adminToken, c.body); code != want {
		t.Fatalf("%s %s = %d, want %d: %s", c.method, c.path, code, want, got)
	}
}

// removalRoutes are the routes in the lock info of a manual lock.
type removalRoutes struct {
	remove string
	force  string
}

// refusedRoutes puts the manual lock at path, sends LOCK to the http backend
// and returns the remove and force-unlock routes of its 423 answer.
func refusedRoutes(t *testing.T, f fixture, path string) removalRoutes {
	t.Helper()

	mustDo(t, f, call{method: http.MethodPut, path: path, body: `{"reason":"freeze"}`}, http.StatusOK)

	code, got := tofuDo(t, f, "LOCK", tofuLockBody)
	if code != http.StatusLocked {
		t.Fatalf("LOCK under a manual lock = %d, want 423: %s", code, got)
	}

	// The http backend reads the PascalCase key Info; a map keeps the key as it is written.
	var info map[string]string
	if err := json.Unmarshal([]byte(got), &info); err != nil {
		t.Fatalf("lock info %q: %v", got, err)
	}

	routes := map[string]string{}
	for _, m := range lockRoute.FindAllStringSubmatch(info["Info"], -1) {
		routes[m[1]] = m[2]
	}

	if routes[http.MethodDelete] == "" || routes[http.MethodPost] == "" {
		t.Fatalf("lock info %q names no DELETE and POST route", info["Info"])
	}

	return removalRoutes{remove: routes[http.MethodDelete], force: routes[http.MethodPost]}
}

// tofuDo sends one request of the http backend to the state address of
// acme/web, with the admin token in basic authentication.
func tofuDo(t *testing.T, f fixture, method, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+"/tofu/state/acme/web",
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:"+adminToken)))

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}

	data, err := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, string(data)
}
