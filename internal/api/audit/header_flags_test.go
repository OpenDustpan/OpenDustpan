package audit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	apitaudit "opendustpan.org/opendustpan/internal/api/audit"
)

// headerBehavior is what a route of the audit API does with the three
// conditional-request headers. The audit routes are outside the description of
// the tool-neutral REST API, so no generated client reads a flag for them.
// This table is the record of their behavior, and the test below compares it
// with the handler.
type headerBehavior struct {
	// etag is true when a success answer holds the ETag header.
	etag bool
	// ifMatch is true when the route honors the If-Match header.
	ifMatch bool
	// idempotencyKey is true when the route replays the answer for a repeated
	// Idempotency-Key header.
	idempotencyKey bool
}

// TestHeaderFlagsMatchTheHandler proves, for each route of the audit adapter,
// what the handler does with the ETag, If-Match and Idempotency-Key headers.
// The adapter serves only reads outside the description, so a row says that
// the route sets no ETag, ignores a stale If-Match and ignores the key. A
// route without a row fails the test, so a route cannot enter without a
// decision about its headers. A row that claims a header behavior fails too,
// until the test has a scenario for it.
func TestHeaderFlagsMatchTheHandler(t *testing.T) {
	t.Parallel()

	want := map[string]headerBehavior{
		"GET /api/audit/entries":            {etag: false, ifMatch: false, idempotencyKey: false},
		"GET /api/audit/entries/{sequence}": {etag: false, ifMatch: false, idempotencyKey: false},
		"GET /api/audit/check":              {etag: false, ifMatch: false, idempotencyKey: false},
		"GET /api/audit/export":             {etag: false, ifMatch: false, idempotencyKey: false},
	}

	routes := newTestHandler(t).Routes()
	if len(routes) != len(want) {
		t.Fatalf("the handler serves %d routes, the test has %d rows", len(routes), len(want))
	}

	for _, rt := range routes {
		pattern := rt.Method + " " + rt.Path

		row, ok := want[pattern]
		if !ok {
			t.Fatalf("route %s has no header row", pattern)
		}

		t.Run(pattern, func(t *testing.T) {
			t.Parallel()

			if row.ifMatch || row.idempotencyKey {
				t.Fatalf("route %s: add a scenario for If-Match or Idempotency-Key, "+
					"as internal/api/jobs/header_flags_test.go does", pattern)
			}

			checkRouteHeaders(t, rt, row)
		})
	}
}

// checkRouteHeaders sends the route three requests: a plain one, one with a
// stale If-Match, and one with an Idempotency-Key. It compares the answers
// with the row.
func checkRouteHeaders(t *testing.T, rt apitaudit.Route, row headerBehavior) {
	t.Helper()

	send := func(headers map[string]string) *httptest.ResponseRecorder {
		handler, store := newEnforcingHandler(t, selectiveAuthorizer{deny: nil})
		seedEntry(t, store, "acme", "alice")

		req := routeRequest(t, rt, "test-token")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		return rec
	}

	plain := send(nil)
	if plain.Code != http.StatusOK {
		t.Fatalf("%s = %d %s, want 200", rt.Path, plain.Code, plain.Body.String())
	}

	if has := plain.Header().Get("ETag") != ""; has != row.etag {
		t.Errorf("%s: ETag header present = %t, row says %t", rt.Path, has, row.etag)
	}

	stale := send(map[string]string{"If-Match": `"stale-1000"`})
	if stale.Code != plain.Code {
		t.Errorf("%s with a stale If-Match = %d, want %d: the handler reads a header that the row does not show",
			rt.Path, stale.Code, plain.Code)
	}

	keyed := send(map[string]string{"Idempotency-Key": "header-test-key-1"})
	if keyed.Code != plain.Code {
		t.Errorf("%s with an Idempotency-Key = %d, want %d: the handler reads a header that the row does not show",
			rt.Path, keyed.Code, plain.Code)
	}

	if keyed.Header().Get("Idempotent-Replayed") != "" {
		t.Errorf("%s answers an Idempotency-Key as a replay, but the row shows no key", rt.Path)
	}
}
