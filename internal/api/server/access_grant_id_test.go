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

// TestRevokeGrantRefusesAnIDThatIsNotAGrantID sends hostile text as the grant
// ID of each revoke route. Each answers 404 with a body that does not hold
// the text, and writes one failure entry that names "grant:invalid", never
// the text. A revoke of an unknown, valid ID writes an entry too.
func TestRevokeGrantRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	paths := []string{"/api/server/grants/", "/api/server/namespaces/acme/grants/"}

	for _, path := range paths {
		for _, id := range accesstest.HostileGrantIDs {
			t.Run(path+id, func(t *testing.T) {
				t.Parallel()

				f := newAccessFixture(t)

				req := httptest.NewRequestWithContext(
					t.Context(), http.MethodDelete, path+url.PathEscape(id), http.NoBody,
				)
				req.Header.Set("Authorization", "Bearer alice")

				rec := httptest.NewRecorder()
				f.handler.ServeHTTP(rec, req)

				if rec.Code != http.StatusNotFound && rec.Code != http.StatusMovedPermanently {
					t.Fatalf("revoke %q answers %d, want 404 (%s)", id, rec.Code, rec.Body.String())
				}

				if len(id) > 8 && strings.Contains(rec.Body.String(), id) {
					t.Fatalf("the body of the refusal holds the ID %q: %s", id, rec.Body.String())
				}

				if rec.Code != http.StatusNotFound {
					return
				}

				listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
				if err != nil {
					t.Fatalf("list audit: %v", err)
				}

				found := false

				for i := range listing.Entries {
					e := listing.Entries[i]
					if len(id) > 8 && strings.Contains(e.Object, id) {
						t.Fatalf("an audit entry holds the ID %q: %q", id, e.Object)
					}

					if e.Object == "grant:invalid" && e.Outcome == audit.OutcomeFailure {
						found = true
					}
				}

				if !found && id != "" && strings.HasPrefix(path, "/api/server/grants/") {
					t.Fatalf("no failure entry for grant:invalid after the revoke of %q", id)
				}
			})
		}
	}
}

// TestRevokeUnknownGrantWritesAnEntry proves that the 404 of DELETE
// /api/server/grants/{id} for a valid ID that names no grant writes a failure
// entry.
func TestRevokeUnknownGrantWritesAnEntry(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/server/grants/grt-unknown-id", http.NoBody)
	req.Header.Set("Authorization", "Bearer alice")

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	for i := range listing.Entries {
		if listing.Entries[i].Object == "grant:grt-unknown-id" && listing.Entries[i].Outcome == audit.OutcomeFailure {
			return
		}
	}

	t.Fatal("no failure entry for the unknown grant")
}

// TestRevokeByIDServeRefusesAnIDThatIsNotAGrantID calls the serve function of
// DELETE /api/server/grants/{id} with no resolver in front of it, as if the
// resolver failed to check the ID. The serve function answers 404 and writes
// a failure entry that names "grant:invalid". The test fails when the check
// of the serve function is removed.
func TestRevokeByIDServeRefusesAnIDThatIsNotAGrantID(t *testing.T) {
	t.Parallel()

	for _, id := range accesstest.HostileGrantIDs {
		f := newAccessFixture(t)

		var serve apiserver.RouteHandler

		for _, route := range f.handler.Routes() {
			if route.Method == http.MethodDelete && route.Path == "/api/server/grants/{id}" {
				serve = route.Serve
			}
		}

		if serve == nil {
			t.Fatal("no DELETE /api/server/grants/{id} route")
		}

		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/api/server/grants/x", http.NoBody)
		req.SetPathValue("id", id)

		rec := httptest.NewRecorder()
		serve(rec, req, identity.Principal{Subject: "alice", Admin: true}, identity.ServerObject())

		if rec.Code != http.StatusNotFound {
			t.Fatalf("serve %.20q answers %d, want 404 (%s)", id, rec.Code, rec.Body.String())
		}

		if len(id) > 3 && strings.Contains(rec.Body.String(), id) {
			t.Fatalf("the body holds the ID: %.80s", rec.Body.String())
		}

		listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}

		found := false

		for i := range listing.Entries {
			if listing.Entries[i].Object == "grant:invalid" && listing.Entries[i].Outcome == audit.OutcomeFailure {
				found = true
			}
		}

		if !found {
			t.Fatalf("no failure entry for grant:invalid after serve of %.20q", id)
		}
	}
}
