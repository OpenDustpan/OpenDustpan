package pulumi_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
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

// TestAuthorizationHeaderNeverFallsBackToTheCookie proves that a request
// with an Authorization header uses only the header: a refused header
// answers 401 even with a valid session cookie.
func TestAuthorizationHeaderNeverFallsBackToTheCookie(t *testing.T) {
	t.Parallel()

	h, secret := cookieHandler(t, aliceChecker(t))

	for _, header := range []string{"token bad", "Bearer x", "token "} {
		req := cookieRequest(t, http.MethodGet, "/api/user", secret)
		req.Header.Set("Authorization", header)

		if rec := serve(h, req); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET /api/user with a valid cookie and %q = %d %s, want 401",
				header, rec.Code, rec.Body.String())
		}
	}
}

// sessionPost returns a POST to target with the session cookie of secret,
// its CSRF value and body.
func sessionPost(t *testing.T, target string, secret session.Secret, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(
		requestid.WithContext(t.Context(), "req-session"), http.MethodPost, target, strings.NewReader(body),
	)
	req.AddCookie(&http.Cookie{
		Name: sessionauth.Cookie, Value: secret.Reveal(), Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	req.Header.Set(sessionauth.CSRFHeader, session.CSRFValue(secret))
	req.Header.Set("Content-Type", "application/json")

	return req
}

// TestSessionCannotStartAnUpdateOrUseTheSecretsProvider proves that a
// console session gets 403 on the routes that create and start an update,
// and on the routes of the secrets provider of the Pulumi CLI.
func TestSessionCannotStartAnUpdateOrUseTheSecretsProvider(t *testing.T) {
	t.Parallel()

	f := newRevealFixtureWithSessions(
		t,
		allowAll(),
		fixtureAudit{
			store: &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}, onFail: nil,
			logger: slog.New(slog.DiscardHandler),
		},
		aliceChecker(t),
	)

	secret, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	const stackPath = "/api/stacks/acme/web/prod"

	cases := []struct {
		path string
		want string
	}{
		{"/update", "cannot start an update"},
		{"/preview", "cannot start an update"},
		{"/refresh", "cannot start an update"},
		{"/destroy", "cannot start an update"},
		{"/update/u-1", "cannot start an update"},
		{"/encrypt", "cannot encrypt or decrypt secrets"},
		{"/batch-encrypt", "cannot encrypt or decrypt secrets"},
		{"/decrypt", "cannot encrypt or decrypt secrets"},
		{"/batch-decrypt", "cannot encrypt or decrypt secrets"},
		{"/decrypt/log-decryption", "cannot encrypt or decrypt secrets"},
		{"/decrypt/log-batch-decryption", "cannot encrypt or decrypt secrets"},
	}

	for _, c := range cases {
		rec := serve(f.handler, sessionPost(t, stackPath+c.path, secret, "{}"))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("POST %s with a session = %d %s, want 403 with %q", c.path, rec.Code, rec.Body.String(), c.want)
		}
	}
}

// TestSessionRevealRecordsTheConsoleChannel proves that the audit entry of
// a reveal by a console session has the channel console, also when the
// request declares another channel.
func TestSessionRevealRecordsTheConsoleChannel(t *testing.T) {
	t.Parallel()

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
	f := newRevealFixtureWithSessions(
		t,
		allowAll(),
		fixtureAudit{store: entries, onFail: nil, logger: slog.New(slog.DiscardHandler)},
		aliceChecker(t),
	)

	_, path := seedRevealVersion(t, f, "service")

	secret, err := session.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	req := sessionPost(t, revealTarget, secret, revealBody(t, path))
	req.Header.Set("Dustpan-Channel", "api")

	if rec := serve(f.handler, req); rec.Code != http.StatusOK {
		t.Fatalf("reveal with a session = %d %s, want 200", rec.Code, rec.Body.String())
	}

	entries.mu.Lock()
	got := slices.Clone(entries.entries)
	entries.mu.Unlock()

	if len(got) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(got))
	}

	if got[0].Channel != audit.ChannelConsole || got[0].Subject != "corp:alice" || got[0].TokenID != "" {
		t.Fatalf("entry channel %q subject %q token %q, want console, corp:alice and no token",
			got[0].Channel, got[0].Subject, got[0].TokenID)
	}
}
