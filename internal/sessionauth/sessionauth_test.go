package sessionauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

const consoleOrigin = "https://dustpan.example"

var errStoreDown = errors.New("store down")

// fakeSessions answers a fixed match or error, and counts its calls.
type fakeSessions struct {
	err   error
	match session.Match
	calls int
}

func (f *fakeSessions) Authenticate(context.Context, session.Secret) (session.Match, error) {
	f.calls++

	return f.match, f.err
}

func aliceMatch() session.Match {
	return session.Match{
		Session: session.Session{
			CreatedAt: time.Time{}, LastSeenAt: time.Time{}, AuthTime: time.Time{}, EndedAt: time.Time{},
			ID: "ses-1", PrincipalID: "prn-1", Provider: "corp", EndReason: "", SourceIP: "", UserAgent: "",
			Generation: 1,
		},
		Owner: session.Owner{
			ActiveAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-1", Subject: "corp:alice", DisplayName: "Alice",
			Kind: principal.KindPerson, BreakGlass: false,
		},
		Digest:     session.Digest{},
		Policy:     session.DefaultPolicy(),
		Accounts:   session.AccountPolicy{Inactivity: 0, DisableAccounts: false},
		Generation: 1,
	}
}

func newSecret(t *testing.T) session.Secret {
	t.Helper()

	s, err := session.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	return s
}

func newChecker(t *testing.T, f *fakeSessions) *sessionauth.Checker {
	t.Helper()

	c, err := sessionauth.New(f, []string{consoleOrigin})
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}

	return c
}

// request returns a request of method with the cookie of secret, when it
// is not empty, and the headers.
func request(t *testing.T, method, secret string, headers map[string]string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), method, "/api/inventory/namespaces", http.NoBody)
	if secret != "" {
		r.AddCookie(&http.Cookie{
			Name: sessionauth.Cookie, Value: secret, Path: "/", Secure: true, HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}

	for k, v := range headers {
		r.Header.Set(k, v)
	}

	return r
}

func TestNewNeedsSessions(t *testing.T) {
	t.Parallel()

	if _, err := sessionauth.New(nil, nil); err == nil {
		t.Fatal("New with no sessions: err = nil")
	}
}

func TestNilCheckerIsUnauthenticated(t *testing.T) {
	t.Parallel()

	var c *sessionauth.Checker

	_, _, err := c.Authenticate(request(t, http.MethodGet, newSecret(t).Reveal(), nil))
	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("nil checker: err = %v, want ErrUnauthenticated", err)
	}
}

// authCase is one request of TestAuthenticate. A nil want expects the
// session of aliceMatch. wantStore tells whether the request reaches the
// store.
type authCase struct {
	headers   map[string]string
	storeErr  error
	want      error
	name      string
	method    string
	cookie    string
	wantStore bool
}

func authCases(secret session.Secret) []authCase {
	raw := secret.Reveal()
	csrf := session.CSRFValue(secret)
	none := map[string]string{}

	return []authCase{
		{
			name: "GET with the cookie", method: http.MethodGet, cookie: raw, headers: none, storeErr: nil, want: nil,
			wantStore: true,
		},
		{
			name: "POST with the CSRF value and the console origin", method: http.MethodPost, cookie: raw,
			headers: map[string]string{sessionauth.CSRFHeader: csrf, "Origin": consoleOrigin}, storeErr: nil,
			want: nil, wantStore: true,
		},
		{
			name: "no cookie", method: http.MethodGet, cookie: "", headers: none, storeErr: nil,
			want: identity.ErrUnauthenticated, wantStore: false,
		},
		{
			name: "a cookie of the wrong form", method: http.MethodGet, cookie: "odps_short", headers: none,
			storeErr: nil, want: identity.ErrUnauthenticated, wantStore: false,
		},
		{
			name: "another origin", method: http.MethodGet, cookie: raw,
			headers: map[string]string{"Origin": "https://evil.example"}, storeErr: nil,
			want: sessionauth.ErrOtherSite, wantStore: false,
		},
		{
			name: "a cross-site request with no origin", method: http.MethodGet, cookie: raw,
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"}, storeErr: nil,
			want: sessionauth.ErrOtherSite, wantStore: false,
		},
		{
			name: "POST with the CSRF value and same-origin Fetch Metadata, no origin", method: http.MethodPost,
			cookie: raw, headers: map[string]string{sessionauth.CSRFHeader: csrf, "Sec-Fetch-Site": "same-origin"},
			storeErr: nil, want: nil, wantStore: true,
		},
		{
			name: "POST with the CSRF value and no origin and no Fetch Metadata", method: http.MethodPost, cookie: raw,
			headers: map[string]string{sessionauth.CSRFHeader: csrf}, storeErr: nil,
			want: sessionauth.ErrOtherSite, wantStore: false,
		},
		{
			name: "POST with the CSRF value and a same-site Fetch Metadata, no origin", method: http.MethodPost,
			cookie: raw, headers: map[string]string{sessionauth.CSRFHeader: csrf, "Sec-Fetch-Site": "same-site"},
			storeErr: nil, want: sessionauth.ErrOtherSite, wantStore: false,
		},
		{
			name: "POST with the CSRF value and a none Fetch Metadata, no origin", method: http.MethodPost,
			cookie: raw, headers: map[string]string{sessionauth.CSRFHeader: csrf, "Sec-Fetch-Site": "none"},
			storeErr: nil, want: sessionauth.ErrOtherSite, wantStore: false,
		},
		{
			name: "POST with the CSRF value and a cross-site request, no origin", method: http.MethodPost, cookie: raw,
			headers: map[string]string{sessionauth.CSRFHeader: csrf, "Sec-Fetch-Site": "cross-site"}, storeErr: nil,
			want: sessionauth.ErrOtherSite, wantStore: false,
		},
		{
			name: "DELETE with the CSRF value and no origin and no Fetch Metadata", method: http.MethodDelete,
			cookie: raw, headers: map[string]string{sessionauth.CSRFHeader: csrf}, storeErr: nil,
			want: sessionauth.ErrOtherSite, wantStore: false,
		},
		{
			name: "HEAD with no origin and no Fetch Metadata", method: http.MethodHead, cookie: raw, headers: none,
			storeErr: nil, want: nil, wantStore: true,
		},
		{
			name: "POST with no CSRF value", method: http.MethodPost, cookie: raw,
			headers: map[string]string{"Origin": consoleOrigin}, storeErr: nil,
			want: sessionauth.ErrCSRF, wantStore: false,
		},
		{
			name: "DELETE with a wrong CSRF value", method: http.MethodDelete, cookie: raw,
			headers: map[string]string{sessionauth.CSRFHeader: "wrong", "Origin": consoleOrigin}, storeErr: nil,
			want: sessionauth.ErrCSRF, wantStore: false,
		},
		{
			name: "a session that the store refuses", method: http.MethodGet, cookie: raw, headers: none,
			storeErr: session.ErrUnauthenticated, want: identity.ErrUnauthenticated, wantStore: true,
		},
		{
			name: "a store failure", method: http.MethodGet, cookie: raw, headers: none, storeErr: errStoreDown,
			want: errStoreDown, wantStore: true,
		},
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()

	secret := newSecret(t)

	for _, tc := range authCases(secret) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &fakeSessions{err: tc.storeErr, match: aliceMatch(), calls: 0}

			p, caller, err := newChecker(t, f).Authenticate(request(t, tc.method, tc.cookie, tc.headers))

			if got := f.calls > 0; got != tc.wantStore {
				t.Errorf("store read = %t, want %t", got, tc.wantStore)
			}

			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}

				return
			}

			checkAlice(t, err, p, caller, secret)
		})
	}
}

// checkAlice checks that a request authenticated as the session of
// aliceMatch with secret.
func checkAlice(t *testing.T, err error, p identity.Principal, caller sessionauth.Caller, secret session.Secret) {
	t.Helper()

	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	if p.Subject != "corp:alice" || p.DisplayName != "Alice" || p.Admin || p.TokenID != "" {
		t.Fatalf("principal = %+v, want corp:alice with no admin and no token", p)
	}

	if p.Kind != identity.KindPerson || p.Credential != identity.CredentialSession || !p.ActsAsPerson() {
		t.Fatalf("kind, credential = %q, %q; want person, session", p.Kind, p.Credential)
	}

	if caller.Match.Session.ID != "ses-1" || caller.Secret.Reveal() != secret.Reveal() {
		t.Fatalf("caller = session %q, want ses-1 with the cookie secret", caller.Match.Session.ID)
	}
}

func TestWithSessionMarksTheContext(t *testing.T) {
	t.Parallel()

	if sessionauth.FromSession(t.Context()) {
		t.Fatal("FromSession of a plain context = true, want false")
	}

	if !sessionauth.FromSession(sessionauth.WithSession(t.Context())) {
		t.Fatal("FromSession of a marked context = false, want true")
	}
}

func TestAuditChannel(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
	req.Header.Set("Dustpan-Channel", "console")

	if got := sessionauth.AuditChannel(req, "Dustpan-Channel"); got != audit.ChannelConsole {
		t.Fatalf("channel of a token request that declares console = %q, want console", got)
	}

	req.Header.Set("Dustpan-Channel", "api")

	if got := sessionauth.AuditChannel(req, "Dustpan-Channel"); got != audit.ChannelAPI {
		t.Fatalf("channel of a token request that declares api = %q, want api", got)
	}

	marked := req.WithContext(sessionauth.WithSession(req.Context()))

	if got := sessionauth.AuditChannel(marked, "Dustpan-Channel"); got != audit.ChannelConsole {
		t.Fatalf("channel of a session request that declares api = %q, want console", got)
	}
}

// TestRefuseDevSubjects proves the layer of the session cookie check: with
// the option, the session of a dev person authenticates nothing, whatever the
// case of the subject, and the sessions of other people pass. Without the
// option, the session of a dev person passes, as on a dev installation.
func TestRefuseDevSubjects(t *testing.T) {
	t.Parallel()

	secret := newSecret(t)

	for _, tc := range []struct {
		subject string
		refuse  bool
		want    error
	}{
		{subject: "dev:admin", refuse: true, want: identity.ErrUnauthenticated},
		{subject: "DEV:admin", refuse: true, want: identity.ErrUnauthenticated},
		{subject: "dev:admin", refuse: false, want: nil},
		{subject: "corp:alice", refuse: true, want: nil},
		{subject: "developer", refuse: true, want: nil},
	} {
		match := aliceMatch()
		match.Owner.Subject = tc.subject

		var opts []sessionauth.Option
		if tc.refuse {
			opts = append(opts, sessionauth.RefuseDevSubjects())
		}

		c, err := sessionauth.New(&fakeSessions{err: nil, match: match, calls: 0}, []string{consoleOrigin}, opts...)
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}

		p, _, err := c.Authenticate(request(t, http.MethodGet, secret.Reveal(), nil))
		if !errors.Is(err, tc.want) {
			t.Errorf("subject %q, refuse %t: err = %v, want %v", tc.subject, tc.refuse, err, tc.want)
		}

		if tc.want != nil && p.Subject != "" {
			t.Errorf("subject %q: a refused session returned the principal %q", tc.subject, p.Subject)
		}
	}
}

// TestOnDevRefused proves that the Checker tells the hook the subject of each
// dev person that it refuses, and only of those.
func TestOnDevRefused(t *testing.T) {
	t.Parallel()

	secret := newSecret(t)

	for _, tc := range []struct {
		subject string
		want    []string
	}{
		{subject: "dev:admin", want: []string{"dev:admin"}},
		{subject: "DEV:Admin", want: []string{"DEV:Admin"}},
		{subject: "corp:alice", want: nil},
		{subject: "developer", want: nil},
	} {
		match := aliceMatch()
		match.Owner.Subject = tc.subject

		var got []string

		c, err := sessionauth.New(&fakeSessions{err: nil, match: match, calls: 0}, []string{consoleOrigin},
			sessionauth.RefuseDevSubjects(),
			sessionauth.OnDevRefused(func(_ context.Context, subject string) { got = append(got, subject) }))
		if err != nil {
			t.Fatalf("new checker: %v", err)
		}

		_, _, err = c.Authenticate(request(t, http.MethodGet, secret.Reveal(), nil))
		if refused := errors.Is(err, identity.ErrUnauthenticated); refused != (len(tc.want) > 0) {
			t.Errorf("subject %q: refused = %t, want %t", tc.subject, refused, len(tc.want) > 0)
		}

		if !slices.Equal(got, tc.want) {
			t.Errorf("subject %q: hook got %v, want %v", tc.subject, got, tc.want)
		}
	}
}

// TestFresh proves the freshness rule of a sign-in: the age is at most the
// window, a sign-in after now has a negative age and is never fresh, and a
// window of 0 turns the check off.
func TestFresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 21, 12, 0, 0, 0, time.UTC)
	window := 15 * time.Minute

	for _, tc := range []struct {
		name     string
		authTime time.Time
		window   time.Duration
		want     bool
	}{
		{name: "signed in now", authTime: now, window: window, want: true},
		{name: "inside the window", authTime: now.Add(-10 * time.Minute), window: window, want: true},
		{name: "at the end of the window", authTime: now.Add(-window), window: window, want: true},
		{name: "after the window", authTime: now.Add(-window - time.Millisecond), window: window, want: false},
		{name: "one millisecond after now", authTime: now.Add(time.Millisecond), window: window, want: false},
		{name: "an hour after now", authTime: now.Add(time.Hour), window: window, want: false},
		{name: "a zero sign-in time", authTime: time.Time{}, window: window, want: false},
		{name: "window of 0 passes an old sign-in", authTime: now.Add(-24 * time.Hour), window: 0, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := sessionauth.Fresh(now, tc.authTime, tc.window); got != tc.want {
				t.Fatalf("Fresh(now, %v, %v) = %v, want %v", tc.authTime, tc.window, got, tc.want)
			}
		})
	}
}
