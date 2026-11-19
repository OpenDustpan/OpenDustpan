package inventory_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// unitAnswer is the whole answer to one request: status, body and headers.
type unitAnswer struct {
	code   int
	body   string
	header http.Header
}

// unitAnswerOf returns the answer in rec without the headers that carry a
// per-request value.
func unitAnswerOf(rec *httptest.ResponseRecorder) unitAnswer {
	h := rec.Header().Clone()
	h.Del("X-Request-ID")
	h.Del("Date")

	return unitAnswer{code: rec.Code, body: rec.Body.String(), header: h}
}

func (a unitAnswer) equal(b unitAnswer) bool {
	return a.code == b.code && a.body == b.body && maps.EqualFunc(a.header, b.header, slices.Equal)
}

// TestUnitByIDHostileRows proves that a caller without unit:read gets one
// answer, in status, body and headers, for an existing unit, an unknown ID, a
// unit of another namespace, the ID of a deleted unit and the ID of a unit of
// another tool: the answer does not show which IDs exist, in which namespace
// or of which kind. A malformed ID answers the same 400 to a denied and to an
// allowed caller, because the syntax check refuses it before any lookup.
func TestUnitByIDHostileRows(t *testing.T) {
	t.Parallel()

	// The caller reads the namespace acme and no unit.
	denied := newUnitHandler(t, grantAuthorizer{allow: map[string]bool{
		grant(identity.ActionNamespaceRead, "acme"): true,
	}})

	want := unitAnswerOf(getUnitByID(t, denied, acmeUnitID, "test-token"))
	if want.code != http.StatusNotFound {
		t.Fatalf("existing unit, denied = %d %s, want 404", want.code, want.body)
	}

	rows := map[string]string{
		"unknown ID":                  "stk-unknown",
		"unit of another namespace":   globexUnitID,
		"deleted unit":                deletedUnitByID,
		"ID of another tool's prefix": "ws-1",
	}

	for name, id := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := unitAnswerOf(getUnitByID(t, denied, id, "test-token"))
			if !got.equal(want) {
				t.Fatalf("answer = %+v, want %+v", got, want)
			}
		})
	}

	t.Run("malformed ID", func(t *testing.T) {
		t.Parallel()

		allowed := newUnitHandler(t, grantAuthorizer{allow: map[string]bool{
			grant(identity.ActionNamespaceRead, "acme"):            true,
			grant(identity.ActionUnitRead, "pulumi:acme/web/prod"): true,
		}})

		for _, id := range []string{"stk%00x", "..%2F..%2Fx", "a%20b"} {
			d := unitAnswerOf(getUnitByID(t, denied, id, "test-token"))
			a := unitAnswerOf(getUnitByID(t, allowed, id, "test-token"))

			if !d.equal(a) {
				t.Fatalf("id %q: denied %d %q, allowed %d %q; want equal", id, d.code, d.body, a.code, a.body)
			}
		}
	})
}
