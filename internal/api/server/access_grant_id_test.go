package server_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// grantServerEntries returns the entries of the installation scope.
func grantServerEntries(t *testing.T, f accessFixture) []audit.Entry {
	t.Helper()

	listing, err := f.store.List(
		t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize},
	)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	return listing.Entries
}

// hasFailureEntry reports whether entries holds a failure entry of object.
func hasFailureEntry(entries []audit.Entry, object string) bool {
	for i := range entries {
		if entries[i].Object == object && entries[i].Outcome == audit.OutcomeFailure {
			return true
		}
	}

	return false
}

// holdsText reports whether text holds id. A short id matches by chance in
// any text, so only an ID of more than 8 bytes counts.
func holdsText(text, id string) bool {
	return len(id) > 8 && strings.Contains(text, id)
}

// TestRevokeGrantRefusesAnIDThatIsNotAGrantID sends hostile text as the grant
// ID of each revoke route. Each answers 404 with a body that does not hold
// the text. DELETE /api/server/grants/{id} writes one failure entry that
// names "grant:invalid", never the text. The test fails when the check of the
// resolver or of the handler is removed.
func TestRevokeGrantRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/api/server/grants/", "/api/server/namespaces/acme/grants/"} {
		for _, id := range accesstest.HostileGrantIDs() {
			t.Run(path+id, func(t *testing.T) {
				t.Parallel()

				f := newAccessFixture(t)
				req := httptest.NewRequestWithContext(
					t.Context(), http.MethodDelete, path+url.PathEscape(id), http.NoBody,
				)
				req.Header.Set("Authorization", "Bearer alice")

				rec := httptest.NewRecorder()
				f.handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusNotFound {
					t.Fatalf("revoke %.20q answers %d, want 404 (%s)", id, rec.Code, rec.Body.String())
				}

				if holdsText(rec.Body.String(), id) {
					t.Fatalf("the body of the refusal holds the ID: %.80s", rec.Body.String())
				}

				checkRefusalEntries(t, f, path, id)
			})
		}
	}
}

// checkRefusalEntries checks the audit entries after the refusal of the
// revoke of id on path.
func checkRefusalEntries(t *testing.T, f accessFixture, path, id string) {
	t.Helper()

	entries := grantServerEntries(t, f)

	for i := range entries {
		if holdsText(entries[i].Object, id) {
			t.Fatalf("an audit entry holds the ID: %.80q", entries[i].Object)
		}
	}

	if id != "" && path == "/api/server/grants/" && !hasFailureEntry(entries, "grant:invalid") {
		t.Fatalf("no failure entry for grant:invalid after the revoke of %.20q", id)
	}
}

// TestRevokeUnknownGrantWritesAnEntry proves that the 404 of DELETE
// /api/server/grants/{id} for a valid ID that names no grant writes a failure
// entry.
func TestRevokeUnknownGrantWritesAnEntry(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodDelete, "/api/server/grants/grt-unknown-id", http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer alice")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	if !hasFailureEntry(grantServerEntries(t, f), "grant:grt-unknown-id") {
		t.Fatal("no failure entry for the unknown grant")
	}
}

// revokeByIDServe returns the serve function of DELETE /api/server/grants/{id}.
func revokeByIDServe(t *testing.T, f accessFixture) apiserver.RouteHandler {
	t.Helper()

	for _, route := range f.handler.Routes() {
		if route.Method == http.MethodDelete && route.Path == "/api/server/grants/{id}" {
			return route.Serve
		}
	}

	t.Fatal("no DELETE /api/server/grants/{id} route")

	return nil
}

// TestRevokeByIDServeRefusesAnIDThatIsNotAGrantID calls the serve function of
// DELETE /api/server/grants/{id} with no resolver in front of it, as if the
// resolver failed to check the ID. The serve function answers 404 and writes
// a failure entry that names "grant:invalid". The test fails when the check
// of the serve function is removed.
func TestRevokeByIDServeRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	for _, id := range accesstest.HostileGrantIDs() {
		f := newAccessFixture(t)
		serve := revokeByIDServe(t, f)

		p, err := subjectAuthenticator{}.AuthenticateToken(t.Context(), "admin")
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}

		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodDelete, "/api/server/grants/x", http.NoBody,
		)
		req.SetPathValue("id", id)

		rec := httptest.NewRecorder()
		serve(rec, req, p, identity.ServerObject())

		if rec.Code != http.StatusNotFound {
			t.Fatalf("serve %.20q answers %d, want 404 (%s)", id, rec.Code, rec.Body.String())
		}

		if holdsText(rec.Body.String(), id) {
			t.Fatalf("the body holds the ID: %.80s", rec.Body.String())
		}

		if !hasFailureEntry(grantServerEntries(t, f), "grant:invalid") {
			t.Fatalf("no failure entry for grant:invalid after serve of %.20q", id)
		}
	}
}
