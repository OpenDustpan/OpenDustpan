package objectcontext_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// contextAnswer is the whole answer to one request: status, body and headers.
type contextAnswer struct {
	code   int
	body   string
	header http.Header
}

// contextAnswerOf returns the answer in rec without the headers that carry a
// per-request value.
func contextAnswerOf(rec *httptest.ResponseRecorder) contextAnswer {
	h := rec.Header().Clone()
	h.Del("X-Request-ID")
	h.Del("Date")

	return contextAnswer{code: rec.Code, body: rec.Body.String(), header: h}
}

func (a contextAnswer) equal(b contextAnswer) bool {
	return a.code == b.code && a.body == b.body && maps.EqualFunc(a.header, b.header, slices.Equal)
}

// TestUnitContextHostileRows proves that a caller without unit:read gets one
// answer, in status, body and headers, for an existing unit, an unknown ID, a
// unit that is in another namespace and the ID of another kind of object: the
// answer does not show which IDs exist, in which namespace or of which kind.
// A malformed ID answers the same answer to a denied and to an allowed caller.
func TestUnitContextHostileRows(t *testing.T) {
	t.Parallel()

	const base = "/api/dustpan/v1/namespaces/"

	// The caller reads the namespace acme and no unit.
	denied := newHandler(t, newAuthorizer("namespace:read acme"), newFakeStore())
	get := func(h http.Handler, path string) contextAnswer {
		return contextAnswerOf(serve(t, h, request{method: http.MethodGet, path: path, token: testToken, body: ""}))
	}

	want := get(denied, base+"acme/units/"+testUnitID+"/context")
	if want.code != http.StatusNotFound {
		t.Fatalf("existing unit, denied = %d %s, want 404", want.code, want.body)
	}

	rows := map[string]string{
		"unknown ID":                          base + "acme/units/ws-unknown/context",
		"unit of another namespace":           base + "globex/units/" + testUnitID + "/context",
		"absent namespace":                    base + "absent/units/" + testUnitID + "/context",
		"ID of another kind (job)":            base + "acme/units/job-1/context",
		"ID of another kind (workspace run)":  base + "acme/units/run-1/context",
		"ID of another kind (state version)":  base + "acme/units/sv-1/context",
		"ID of another kind (unit of pulumi)": base + "acme/units/stk-1/context",
		"ID with the namespace's own name":    base + "acme/units/acme/context",
	}

	for name, path := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := get(denied, path); !got.equal(want) {
				t.Fatalf("answer = %+v, want %+v", got, want)
			}
		})
	}

	t.Run("malformed ID", func(t *testing.T) {
		t.Parallel()

		allowed := newHandler(t, newAuthorizer(
			"namespace:read acme", "unit:read opentofu:acme/net", "unit:read opentofu:acme/ws-1",
		), newFakeStore())

		for _, id := range []string{"ws%00x", "..%2F..%2Fx", "a%20b"} {
			path := base + "acme/units/" + id + "/context"

			if d, a := get(denied, path), get(allowed, path); !d.equal(a) {
				t.Fatalf("id %q: denied %d %q, allowed %d %q; want equal", id, d.code, d.body, a.code, a.body)
			}
		}
	})
}
