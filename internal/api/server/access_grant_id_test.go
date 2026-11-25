package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// grantServerEntries returns the entries of the installation scope.
func grantServerEntries(t *testing.T, f accessFixture) []audit.Entry {
	t.Helper()

	listing, err := f.store.List(
		t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0},
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

// TestRevokeUnknownGrantBodiesAreIdentical proves that the 404 body of a
// revoke does not tell an ID that is not a grant ID, an ID that names no
// grant and a grant that the caller cannot reach from each other, and that it
// holds no part of the ID. It checks DELETE /api/server/grants/{id} and the
// scoped route on a namespace. The test fails when a refusal puts the ID or
// the object in its text.
func TestRevokeUnknownGrantBodiesAreIdentical(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	send := func(subject, path string) (int, string) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, path, http.NoBody)
		req.Header.Set("Authorization", "Bearer "+subject)

		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)

		return rec.Code, rec.Body.String()
	}

	// hidden is a grant on acme that bob cannot see. inst is a grant on the
	// installation, so the scoped route of acme must not end it.
	var hidden, inst grantDoc

	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/namespaces/acme/grants",
		`{"subject":"principal:prn-olga","role":"viewer"}`, http.StatusCreated,
	}, &hidden)
	f.as(t, "ann", apiCall{
		http.MethodPost, "/api/server/grants", `{"subject":"principal:prn-olga","role":"viewer"}`,
		http.StatusCreated,
	}, &inst)

	cases := []struct {
		name, route, subject, id string
	}{
		{"server route, invalid ID", "/api/server/grants/", "alice", "not-a-grant-id"},
		{"server route, unknown ID", "/api/server/grants/", "alice", "grt-unknown-id"},
		{"server route, grant the caller cannot see", "/api/server/grants/", "bob", hidden.ID},
		{"scoped route, invalid ID", "/api/server/namespaces/acme/grants/", "alice", "not-a-grant-id"},
		{"scoped route, unknown ID", "/api/server/namespaces/acme/grants/", "alice", "grt-unknown-id"},
		{"scoped route, grant on another object", "/api/server/namespaces/acme/grants/", "alice", inst.ID},
	}

	var want string

	for i, tc := range cases {
		status, body := send(tc.subject, tc.route+tc.id)
		if status != http.StatusNotFound {
			t.Fatalf("%s answers %d, want 404 (%s)", tc.name, status, body)
		}

		if holdsText(body, tc.id) || strings.Contains(body, "acme") {
			t.Fatalf("%s: the body holds the ID or the object: %s", tc.name, body)
		}

		if i == 0 {
			want = body
		}

		if body != want {
			t.Fatalf("%s: body = %s, want %s", tc.name, body, want)
		}
	}
}

// TestRevokeByIDServeWritesTheSameEntryForAnObjectWithNoGrantActions calls
// the serve function of DELETE /api/server/grants/{id} with an object that
// has no grant actions. It answers the same 404 as an ID that is not a grant
// ID and writes the same failure entry. The test fails when the entry of
// that path is removed.
func TestRevokeByIDServeWritesTheSameEntryForAnObjectWithNoGrantActions(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"grt-valid-id", "not-a-grant-id"} {
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
		serve(rec, req, p, identity.Object{})

		if rec.Code != http.StatusNotFound {
			t.Fatalf("serve %q answers %d, want 404 (%s)", id, rec.Code, rec.Body.String())
		}

		object := "grant:" + id
		if id == "not-a-grant-id" {
			object = "grant:invalid"
		}

		if !hasFailureEntry(grantServerEntries(t, f), object) {
			t.Fatalf("no failure entry for %s", object)
		}
	}
}

// errDiskGone is a read error that is not a domain error.
var errDiskGone = errors.New("disk is gone")

// TestRevokeGrantReadFailureDoesNotEchoTheID makes the read of the grant fail
// with an error that is not ErrNotFound, for an ID of a valid shape that is
// long enough to find in a body. The body must not hold the ID. The test fails
// when the resolver formats the request ID into the error it returns.
func TestRevokeGrantReadFailureDoesNotEchoTheID(t *testing.T) {
	t.Parallel()

	const id = "grt-hostile.Looking_ID-0123456789-abcdef"

	if !access.ValidGrantID(id) {
		t.Fatalf("the test ID %q is not a valid grant ID", id)
	}

	for name, readErr := range map[string]error{
		"invalid": access.ErrInvalid,
		"plain":   errDiskGone,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixtureAll(t, accessFixtureParts{
				entries: func(st *sqlite.Store) auditStore { return st }, passkeys: nil, tweak: nil,
				links: noGitHostLinks, ports: accessPorts{collections: nil, settings: nil, authzSettings: nil},
				now: nil, logger: nil, noSignIn: false,
				getGrantErr: readErr, afterRevoke: nil, tokenList: nil,
			})

			for _, path := range []string{"/api/server/grants/", "/api/server/namespaces/acme/grants/"} {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, path+id, http.NoBody)
				req.Header.Set("Authorization", "Bearer alice")

				rec := httptest.NewRecorder()
				f.handler.ServeHTTP(rec, req)

				if rec.Code == http.StatusNoContent || rec.Code == http.StatusOK {
					t.Fatalf("revoke on %s answers %d, want a failure", path, rec.Code)
				}

				if strings.Contains(rec.Body.String(), id) {
					t.Fatalf("the body on %s holds the ID: %.200s", path, rec.Body.String())
				}
			}
		})
	}
}
