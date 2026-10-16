package inventory_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
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
			DisabledAt: time.Time{}, ID: "prn-1", Subject: "corp:alice", DisplayName: "Alice",
			Kind: principal.KindPerson,
		},
		Digest: session.Digest{},
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
