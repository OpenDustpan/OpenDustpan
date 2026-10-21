package audit_test

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
// and no Authorization header.
func cookieRequest(t *testing.T, method, target string) *http.Request {
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

	return req
}

// serveCookie serves req with h and returns the answer.
func serveCookie(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestSessionCookieSignsIn(t *testing.T) {
	t.Parallel()

	h, _ := newEnforcingHandlerWithSessions(t, selectiveAuthorizer{deny: map[identity.Action]bool{}}, aliceChecker(t))
	req := cookieRequest(t, http.MethodGet, "/api/audit/entries?organization=acme")

	if rec := serveCookie(h, req); rec.Code != http.StatusOK {
		t.Fatalf("entry list with the session cookie = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestSessionCookieFromAnotherSite(t *testing.T) {
	t.Parallel()

	h, _ := newEnforcingHandlerWithSessions(t, selectiveAuthorizer{deny: map[identity.Action]bool{}}, aliceChecker(t))
	req := cookieRequest(t, http.MethodGet, "/api/audit/entries?organization=acme")
	req.Header.Set("Origin", "https://evil.example")

	rec := serveCookie(h, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "another site") {
		t.Fatalf("entry list from another site = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

func TestNoSessionCheckIgnoresTheCookie(t *testing.T) {
	t.Parallel()

	h, _ := newEnforcingHandlerWithSessions(t, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)
	req := cookieRequest(t, http.MethodGet, "/api/audit/entries?organization=acme")

	if rec := serveCookie(h, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("entry list with the cookie and no session check = %d, want 401", rec.Code)
	}
}

// TestAuthorizationHeaderNeverFallsBackToTheCookie proves that a request
// with an Authorization header uses only the header: a refused header
// answers 401 even with a valid session cookie.
func TestAuthorizationHeaderNeverFallsBackToTheCookie(t *testing.T) {
	t.Parallel()

	h, _ := newEnforcingHandlerWithSessions(t, selectiveAuthorizer{deny: map[identity.Action]bool{}}, aliceChecker(t))

	for _, header := range []string{"token bad", "Bearer x", "token "} {
		req := cookieRequest(t, http.MethodGet, "/api/audit/entries?organization=acme")
		req.Header.Set("Authorization", header)

		if rec := serveCookie(h, req); rec.Code != http.StatusUnauthorized {
			t.Errorf("entry list with a valid cookie and %q = %d %s, want 401", header, rec.Code, rec.Body.String())
		}
	}
}

// TestSessionCookieOnlyServesReads proves that the audit API has no route
// for a method that can change state, so the CSRF check has nothing to
// guard here: a POST with the cookie answers 405 before the cookie check.
func TestSessionCookieOnlyServesReads(t *testing.T) {
	t.Parallel()

	h, _ := newEnforcingHandlerWithSessions(t, selectiveAuthorizer{deny: map[identity.Action]bool{}}, aliceChecker(t))

	for _, target := range []string{"/api/audit/entries?organization=acme", "/api/audit/check?organization=acme"} {
		if rec := serveCookie(h, cookieRequest(t, http.MethodPost, target)); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s with the cookie = %d, want 405", target, rec.Code)
		}
	}
}

// TestBearerTokenSignsIn proves that the audit API takes the Bearer scheme
// as well as the token scheme, as every tool-neutral API does.
func TestBearerTokenSignsIn(t *testing.T) {
	t.Parallel()

	h, _ := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	for _, header := range []string{"token test-token", "Bearer test-token"} {
		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"/api/audit/entries?organization=acme",
			http.NoBody,
		)
		req.Header.Set("Authorization", header)

		if rec := serveCookie(h, req); rec.Code != http.StatusOK {
			t.Errorf("entry list with %q = %d %s, want 200", strings.Fields(header)[0], rec.Code, rec.Body.String())
		}
	}
}

// TestSessionRefusalNamesTheConsole proves that a request signed in by the
// session cookie carries the session: the record of its refusal names the
// console channel, and a refusal of a token request with no channel header
// names the API channel.
func TestSessionRefusalNamesTheConsole(t *testing.T) {
	t.Parallel()

	deny := selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionNamespaceReadAudit: true}}
	h, entries := newEnforcingHandlerWithSessions(t, deny, aliceChecker(t))

	cookie := cookieRequest(t, http.MethodGet, "/api/audit/entries?organization=acme")
	if rec := serveCookie(h, cookie); rec.Code != http.StatusForbidden {
		t.Fatalf("denied entry list with the cookie = %d, want 403", rec.Code)
	}

	token := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/audit/entries?organization=acme",
		http.NoBody,
	)
	token.Header.Set("Authorization", "token test-token")

	if rec := serveCookie(h, token); rec.Code != http.StatusForbidden {
		t.Fatalf("denied entry list with a token = %d, want 403", rec.Code)
	}

	got := map[string]audit.Channel{}

	for _, e := range entries.entries["acme"] {
		if e.Outcome == audit.OutcomeDenied {
			got[e.Subject] = e.Channel
		}
	}

	if got["corp:alice"] != audit.ChannelConsole {
		t.Errorf("channel of the session refusal = %q, want console (entries %v)", got["corp:alice"], got)
	}

	for subject, channel := range got {
		if subject != "corp:alice" && channel != audit.ChannelAPI {
			t.Errorf("channel of the token refusal of %s = %q, want api", subject, channel)
		}
	}
}
