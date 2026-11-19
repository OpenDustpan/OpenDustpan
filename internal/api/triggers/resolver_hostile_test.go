package triggers_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// resolverAnswer is the whole answer of one request: status, body and the
// headers without a value that changes with each request.
type resolverAnswer struct {
	code   int
	body   string
	header http.Header
}

func (a resolverAnswer) equal(b resolverAnswer) bool {
	return a.code == b.code && a.body == b.body && maps.EqualFunc(a.header, b.header, slices.Equal)
}

func answerOfRecorder(rec *httptest.ResponseRecorder) resolverAnswer {
	h := rec.Header().Clone()
	h.Del("X-Request-ID")
	h.Del("Date")

	return resolverAnswer{code: rec.Code, body: rec.Body.String(), header: h}
}

const hostileNS = "/api/dustpan/v1/namespaces/"

// hostileRow is one hostile path: the path up to the unit ID and the trigger
// ID. onlyTrigger marks a row that a unit route cannot use.
type hostileRow struct {
	name        string
	unitDir     string
	trig        string
	onlyTrigger bool
}

func hostileRows() []hostileRow {
	unit := func(ns, id string) string { return hostileNS + ns + "/units/" + id }

	return []hostileRow{
		{name: "unknown trigger ID", unitDir: unit("acme", netID), trig: "trg-nope", onlyTrigger: true},
		{name: "trigger of another unit", unitDir: unit("acme", appID), trig: "trg-seed", onlyTrigger: true},
		{name: "unknown unit ID", unitDir: unit("acme", "ws-404"), trig: "trg-seed", onlyTrigger: false},
		{name: "unit of another namespace", unitDir: unit("acme", otherID), trig: "trg-seed", onlyTrigger: false},
		{name: "unit ID in namespace other", unitDir: unit("other", netID), trig: "trg-seed", onlyTrigger: false},
		{name: "namespace that does not exist", unitDir: unit("absent", netID), trig: "trg-seed", onlyTrigger: false},
		{name: "malformed namespace", unitDir: unit("Bad!NS", netID), trig: "trg-seed", onlyTrigger: false},
		{name: "malformed unit ID", unitDir: unit("acme", "WS%20%2E%2E"), trig: "trg-seed", onlyTrigger: false},
		{
			name: "unit ID over the length limit", unitDir: unit("acme", strings.Repeat("a", 101)),
			trig: "trg-seed", onlyTrigger: false,
		},
		{name: "malformed trigger ID", unitDir: unit("acme", netID), trig: "TRG!%20x", onlyTrigger: true},
		{
			name: "trigger ID over the length limit", unitDir: unit("acme", netID),
			trig: strings.Repeat("a", 300), onlyTrigger: true,
		},
	}
}

// hostileRoute is one route of the handler, with the part of its path after
// the trigger ID.
type hostileRoute struct {
	name   string
	method string
	suffix string
	unit   bool // a route on the unit, with no trigger ID
	body   string
}

func hostileRoutes() []hostileRoute {
	return []hostileRoute{
		{name: "list", method: http.MethodGet, suffix: "", unit: true, body: ""},
		{name: "create", method: http.MethodPost, suffix: "", unit: true, body: scheduleDoc},
		{name: "get", method: http.MethodGet, suffix: "", unit: false, body: ""},
		{name: "replace", method: http.MethodPut, suffix: "", unit: false, body: scheduleDoc},
		{name: "delete", method: http.MethodDelete, suffix: "", unit: false, body: ""},
		{name: "fire", method: http.MethodPost, suffix: "/fire", unit: false, body: ""},
	}
}

// path returns the path of row on the route.
func (rt hostileRoute) path(row hostileRow) string {
	if rt.unit {
		return row.unitDir + "/triggers"
	}

	return row.unitDir + "/triggers/" + row.trig + rt.suffix
}

// TestDeniedCallerSeesOneAnswerForEveryObject proves that the resolvers show a
// caller with no read action nothing about the objects: an unknown trigger, a
// trigger of another unit, a unit of another namespace, an unknown or
// malformed unit ID, a malformed trigger ID and a namespace that does not
// exist all get the status, the body and the headers of an existing trigger
// that the caller may not read (404, as for every denied read).
func TestDeniedCallerSeesOneAnswerForEveryObject(t *testing.T) {
	t.Parallel()

	known := hostileRow{
		name: "existing trigger", unitDir: hostileNS + "acme/units/" + netID, trig: "trg-seed", onlyTrigger: false,
	}

	for _, rt := range hostileRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer())
			seed(r)

			send := func(path string) resolverAnswer {
				return answerOfRecorder(serve(t, r.h, request{
					method: rt.method, path: path, token: testToken, body: rt.body, header: nil,
				}))
			}

			want := send(rt.path(known))
			if want.code != http.StatusNotFound {
				t.Fatalf("existing object = %d %s, want 404", want.code, want.body)
			}

			for _, row := range hostileRows() {
				checkResolverAnswer(t, rt, row, send, want)
			}
		})
	}
}

// checkResolverAnswer fails unless the answer for row on rt is want.
func checkResolverAnswer(
	t *testing.T, rt hostileRoute, row hostileRow, send func(string) resolverAnswer, want resolverAnswer,
) {
	t.Helper()

	if rt.unit && row.onlyTrigger {
		return // a unit route has no trigger ID.
	}

	if got := send(rt.path(row)); !got.equal(want) {
		t.Errorf("%s: %s = %d %q %v, want %d %q %v", row.name, rt.path(row),
			got.code, got.body, got.header, want.code, want.body, want.header)
	}
}
