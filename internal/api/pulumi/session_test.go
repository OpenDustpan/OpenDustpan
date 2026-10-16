package pulumi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/stack"
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

// cookieHandler returns a user handler for the namespace acme with the
// session cookie check sessions, and a session secret.
func cookieHandler(t *testing.T, sessions *sessionauth.Checker) (*pulumi.Handler, session.Secret) {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme")

	secret, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	authz := selectiveAuthorizer{deny: map[identity.Action]bool{}}

	return newUserHandlerWithSessions(t, nsStore, stStore, authz, sessions), secret
}

// cookieRequest returns a request with the session cookie of secret and no
// Authorization header.
func cookieRequest(t *testing.T, method, target string, secret session.Secret) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	req.AddCookie(&http.Cookie{
		Name: sessionauth.Cookie, Value: secret.Reveal(), Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	return req
}

func serve(h *pulumi.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestSessionCookieSignsIn(t *testing.T) {
	t.Parallel()

	h, secret := cookieHandler(t, aliceChecker(t))

	rec := serve(h, cookieRequest(t, http.MethodGet, "/api/user", secret))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "acme") {
		t.Fatalf("GET /api/user with the session cookie = %d %s, want 200 with acme", rec.Code, rec.Body.String())
	}
}

func TestSessionCookieNeedsCSRF(t *testing.T) {
	t.Parallel()

	h, secret := cookieHandler(t, aliceChecker(t))
	target := "/api/stacks/acme/proj/dev/rename"

	rec := serve(h, cookieRequest(t, http.MethodPost, target, secret))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "X-Dustpan-CSRF") {
		t.Fatalf("POST with the cookie and no CSRF value = %d %s, want 403 that names the header",
			rec.Code, rec.Body.String())
	}

	req := cookieRequest(t, http.MethodPost, target, secret)
	req.Header.Set(sessionauth.CSRFHeader, session.CSRFValue(secret))

	if rec := serve(h, req); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("POST with the cookie and the CSRF value = %d %s, want past the sign-in", rec.Code, rec.Body.String())
	}
}

func TestNoSessionCheckIgnoresTheCookie(t *testing.T) {
	t.Parallel()

	h, secret := cookieHandler(t, nil)

	if rec := serve(h, cookieRequest(t, http.MethodGet, "/api/user", secret)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/user with the cookie and no session check = %d, want 401", rec.Code)
	}
}
