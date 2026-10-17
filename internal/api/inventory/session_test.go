package inventory_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// aliceSessions accepts every session as the session of corp:alice.
type aliceSessions struct{}

func (aliceSessions) Authenticate(context.Context, session.Secret) (session.Match, error) {
	return session.Match{
		Session: session.Session{
			CreatedAt: time.Time{}, LastSeenAt: time.Time{}, AuthTime: time.Time{}, EndedAt: time.Time{},
			ID: "ses-1", PrincipalID: "prn-1", Provider: "corp", EndReason: "", SourceIP: "", UserAgent: "",
		},
		Owner: session.Owner{
			ActiveAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-1", Subject: "corp:alice", DisplayName: "Alice",
			Kind: principal.KindPerson, BreakGlass: false,
		},
		Digest:   session.Digest{},
		Policy:   session.DefaultPolicy(),
		Accounts: session.AccountPolicy{Inactivity: 0, DisableAccounts: false},
	}, nil
}

// aliceChecker returns a session cookie check that takes every session as
// the session of corp:alice.
func aliceChecker(t *testing.T) *sessionauth.Checker {
	t.Helper()

	c, err := sessionauth.New(aliceSessions{}, []string{"https://dustpan.example"})
	if err != nil {
		t.Fatalf("session check: %v", err)
	}

	return c
}

// cookieRequest returns a request with the cookie of a new session secret
// and no Authorization header, and the secret.
func cookieRequest(t *testing.T, method, target string) (*http.Request, session.Secret) {
	t.Helper()

	secret, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	req.AddCookie(&http.Cookie{
		Name: sessionauth.Cookie, Value: secret.Reveal(), Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return req, secret
}

// serveCookie serves req with h and returns the answer.
func serveCookie(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// cookieInventory returns the seeded inventory handler with the session
// cookie check sessions.
func cookieInventory(t *testing.T, sessions *sessionauth.Checker) http.Handler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	reader := &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil}
	seedEntries(nsStore, reader)

	return newBehaviorHandlerWithSessions(t, nsStore, reader, selectiveAuthorizer{deny: map[identity.Action]bool{}},
		sessions)
}

func TestSessionCookieSignsIn(t *testing.T) {
	t.Parallel()

	req, _ := cookieRequest(t, http.MethodGet, "/api/inventory/units?organization=acme")

	if rec := serveCookie(cookieInventory(t, aliceChecker(t)), req); rec.Code != http.StatusOK {
		t.Fatalf("unit list with the session cookie = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestSessionCookieNeedsCSRF(t *testing.T) {
	t.Parallel()

	h := cookieInventory(t, aliceChecker(t))
	target := "/api/inventory/deleted-units/pulumi/stk-1/restore"

	req, _ := cookieRequest(t, http.MethodPost, target)

	rec := serveCookie(h, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "X-Dustpan-CSRF") {
		t.Fatalf("POST with the cookie and no CSRF value = %d %s, want 403 that names the header",
			rec.Code, rec.Body.String())
	}

	req, secret := cookieRequest(t, http.MethodPost, target)
	req.Header.Set(sessionauth.CSRFHeader, session.CSRFValue(secret))

	if rec := serveCookie(h, req); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("POST with the cookie and the CSRF value = %d %s, want past the sign-in", rec.Code, rec.Body.String())
	}
}

func TestNoSessionCheckIgnoresTheCookie(t *testing.T) {
	t.Parallel()

	req, _ := cookieRequest(t, http.MethodGet, "/api/inventory/units?organization=acme")

	if rec := serveCookie(cookieInventory(t, nil), req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unit list with the cookie and no session check = %d, want 401", rec.Code)
	}
}

// TestAuthorizationHeaderNeverFallsBackToTheCookie proves that a request
// with an Authorization header uses only the header: a refused header
// answers 401 even with a valid session cookie.
func TestAuthorizationHeaderNeverFallsBackToTheCookie(t *testing.T) {
	t.Parallel()

	h := cookieInventory(t, aliceChecker(t))

	for _, header := range []string{"token bad", "Bearer x", "token "} {
		req, _ := cookieRequest(t, http.MethodGet, "/api/inventory/units?organization=acme")
		req.Header.Set("Authorization", header)

		if rec := serveCookie(h, req); rec.Code != http.StatusUnauthorized {
			t.Errorf("unit list with a valid cookie and %q = %d %s, want 401", header, rec.Code, rec.Body.String())
		}
	}
}

// TestSessionRestoreRecordsTheConsoleChannel proves that the audit entry of
// a restore by a console session has the channel console and the subject of
// the session, also when the request declares another channel.
func TestSessionRestoreRecordsTheConsoleChannel(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	reader := &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil}
	seedEntries(nsStore, reader)

	lifecycle := &memLifecycle{err: nil, calls: nil}
	h := newBehaviorHandlerWith(t, nsStore, reader, selectiveAuthorizer{deny: map[identity.Action]bool{}},
		behaviorWiring{sessions: aliceChecker(t), lifecycles: map[string]coreinventory.Lifecycle{"pulumi": lifecycle}})

	req, secret := cookieRequest(t, http.MethodPost, restoreTarget)
	req.Header.Set(sessionauth.CSRFHeader, session.CSRFValue(secret))
	req.Header.Set("Dustpan-Channel", "api")

	if rec := serveCookie(h, req); rec.Code != http.StatusOK {
		t.Fatalf("restore with a session = %d %s, want 200", rec.Code, rec.Body.String())
	}

	if len(lifecycle.calls) != 1 {
		t.Fatalf("lifecycle calls = %d, want 1", len(lifecycle.calls))
	}

	e := lifecycle.calls[0].entry
	if e.Channel != audit.ChannelConsole || e.Subject != "corp:alice" || e.TokenID != "" {
		t.Fatalf("entry channel %q subject %q token %q, want console, corp:alice and no token",
			e.Channel, e.Subject, e.TokenID)
	}
}

// sessionChange returns a request with the session cookie of secret, its
// CSRF value, a declared channel api and body.
func sessionChange(t *testing.T, method, target string, secret session.Secret, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.AddCookie(&http.Cookie{
		Name: sessionauth.Cookie, Value: secret.Reveal(), Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	req.Header.Set(sessionauth.CSRFHeader, session.CSRFValue(secret))
	req.Header.Set("Dustpan-Channel", "api")

	return req
}

// TestSessionCollectionChangesRecordTheConsoleChannel proves that the audit
// entries of a collection create, a unit move and a collection delete by a
// console session have the channel console, also when the request declares
// another channel.
func TestSessionCollectionChangesRecordTheConsoleChannel(t *testing.T) {
	t.Parallel()

	f := newMoveFixtureWithSessions(t, selectiveAuthorizer{deny: map[identity.Action]bool{}}, allowMoves{},
		aliceChecker(t))

	secret, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	steps := []struct {
		method, target, body string
		want                 int
	}{
		{http.MethodPost, "/api/inventory/namespaces/acme/collections", `{"name":"team"}`, http.StatusCreated},
		{
			http.MethodPut, "/api/inventory/unit-collection?unit=" + f.unitText, `{"collection":"new"}`,
			http.StatusOK,
		},
		{http.MethodDelete, "/api/inventory/namespaces/acme/collections/team", "", http.StatusNoContent},
	}

	for _, s := range steps {
		if rec := serveCookie(f.handler, sessionChange(t, s.method, s.target, secret, s.body)); rec.Code != s.want {
			t.Fatalf("%s %s with a session = %d %s, want %d", s.method, s.target, rec.Code, rec.Body.String(), s.want)
		}
	}

	if len(f.store.entries) != len(steps) {
		t.Fatalf("collection entries = %d, want %d", len(f.store.entries), len(steps))
	}

	for i := range f.store.entries {
		e := f.store.entries[i]
		if e.Channel != audit.ChannelConsole || e.Subject != "corp:alice" || e.TokenID != "" {
			t.Errorf("entry %s: channel %q subject %q token %q, want console, corp:alice and no token",
				e.Action, e.Channel, e.Subject, e.TokenID)
		}
	}
}
