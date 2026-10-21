package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// sessionOf starts a console session of the person "prn-<subject>" that
// signed in at authTime, and returns its cookie and CSRF value.
func (f accessFixture) sessionOf(t *testing.T, subject string, authTime time.Time) (*http.Cookie, string) {
	t.Helper()

	at := f.sessions.Now()
	entry := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: subject, IDPSubject: "", TokenID: "",
		Action: "session:start", Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelConsole, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	_, secret, err := f.sessions.Start(t.Context(), entry, session.Start{
		AuthTime: authTime, PrincipalID: "prn-" + subject, Provider: "corp", SourceIP: "192.0.2.1",
		UserAgent: "test", Stored: false,
	})
	if err != nil {
		t.Fatalf("start the session of %s: %v", subject, err)
	}

	cookie := &http.Cookie{Name: apiserver.SessionCookie, Value: secret.Reveal()}

	return cookie, session.CSRFValue(secret)
}

// adminSeeded holds the stores in which the person "admin" exists.
var adminSeeded sync.Map

// seedSessionAdmin makes the person "admin" with the site administrator role
// when subject is "admin". The bootstrap administrator signs in with an access
// token, which has no console session, so a change that needs a session uses
// this person.
func (f accessFixture) seedSessionAdmin(t *testing.T, subject string) {
	t.Helper()

	if subject != "admin" {
		return
	}

	if _, loaded := adminSeeded.LoadOrStore(f.store, true); !loaded {
		f.seed(t, "admin", access.RoleSiteAdministrator, identity.ServerObject())
	}
}

// asSession sends one request as the console session of subject, with a
// sign-in at authTime, and fails unless it answers c.want. It decodes a JSON
// answer into out when out is not nil.
func (f accessFixture) asSession(t *testing.T, subject string, authTime time.Time, c apiCall, out any) {
	t.Helper()

	f.seedSessionAdmin(t, subject)

	cookie, csrf := f.sessionOf(t, subject, authTime)

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(c.body))
	req.AddCookie(cookie)
	req.Header.Set(apiserver.CSRFHeader, csrf)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	if rec.Code != c.want {
		t.Fatalf("session of %s %s %s answers %d, want %d (%s)", subject, c.method, c.path, rec.Code, c.want,
			rec.Body.String())
	}

	if out != nil {
		decodeInto(t, rec.Body.String(), out)
	}
}

// asFresh sends one request as a console session of subject with a sign-in
// now. An access token never gives the fresh authentication that a change of
// roles or grants needs.
func (f accessFixture) asFresh(t *testing.T, subject string, c apiCall, out any) {
	t.Helper()

	f.asSession(t, subject, f.sessions.Now(), c, out)
}

// needsFreshSession reports whether method and target are a change of roles
// or grants, or a token creation or rotation, that needs a fresh
// authentication.
func needsFreshSession(method, target string) bool {
	path, _, _ := strings.Cut(target, "?")

	switch method {
	case http.MethodPost:
		return path == "/api/server/roles" || path == "/api/server/service-tokens" ||
			path == "/api/server/caller/tokens" || strings.HasSuffix(path, "/tokens") ||
			strings.HasSuffix(path, "/rotate") ||
			(strings.HasSuffix(path, "/grants") && !strings.HasSuffix(path, "/unit-grants"))
	case http.MethodPatch:
		return strings.HasPrefix(path, "/api/server/roles/")
	default:
		return false
	}
}

// storeSessions starts console sessions over st for the person "tester", the
// subject of the test token. An access token never gives the fresh
// authentication that some changes need, so their tests use these sessions.
type storeSessions struct {
	svc        *session.Sessions
	principals *principal.Service
}

// newStoreSessions returns the sessions over st and makes the person
// "prn-tester" in st when it is not there.
func newStoreSessions(t *testing.T, st *sqlite.Store) storeSessions {
	t.Helper()

	svc, err := session.NewSessions(session.SessionsConfig{Store: st, Providers: st, Inactivity: nil, Now: nil})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	prn := principal.Principal{
		CreatedAt: at, UpdatedAt: at, ID: "prn-tester", Subject: "tester", DisplayName: "Tester",
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		BreakGlass: false, DisabledAt: time.Time{},
	}

	err = st.CreatePrincipal(t.Context(), prn, storetest.ServerChange(at, "principal:"+prn.ID))
	if err != nil && !errors.Is(err, principal.ErrAlreadyExists) {
		t.Fatalf("seed the person tester: %v", err)
	}

	principals, err := principal.NewService(principal.Config{
		TempAdmins: nil, Store: st, Guard: allowAllGuard{}, Namespaces: st, Tokens: nil, Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	return storeSessions{svc: svc, principals: principals}
}

// signIn returns the sign-in configuration that reads these sessions.
func (s storeSessions) signIn() *apiserver.SignIn {
	return &apiserver.SignIn{
		OnDevRefused: nil, DevCode: nil, DevAdmin: noBootstrapAdmin, Sessions: s.svc, Limiter: nil,
		Stored: nil, Admin: nil, Origins: []string{signInBase}, Providers: nil,
	}
}

// start adds the cookie and the CSRF value of a new session of "tester" to
// req. The sign-in is age old.
func (s storeSessions) start(t *testing.T, req *http.Request, age time.Duration) {
	t.Helper()

	at := s.svc.Now()
	entry := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: "tester", IDPSubject: "", TokenID: "",
		Action: "session:start", Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelConsole, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	_, secret, err := s.svc.Start(t.Context(), entry, session.Start{
		AuthTime: at.Add(-age), PrincipalID: "prn-tester", Provider: "corp", SourceIP: "192.0.2.1",
		UserAgent: "test", Stored: false,
	})
	if err != nil {
		t.Fatalf("start the session of tester: %v", err)
	}

	req.AddCookie(&http.Cookie{Name: apiserver.SessionCookie, Value: secret.Reveal()})
	req.Header.Set(apiserver.CSRFHeader, session.CSRFValue(secret))
}

// handlerSessions maps a Handler of a test to the sessions of its store.
var handlerSessions sync.Map

// signInOf returns the sign-in configuration that reads the sessions over st,
// and those sessions.
func signInOf(t *testing.T, st *sqlite.Store) (*apiserver.SignIn, storeSessions) {
	t.Helper()

	s := newStoreSessions(t, st)

	return s.signIn(), s
}

// rememberSessions records s as the sessions of h.
func rememberSessions(h *apiserver.Handler, s storeSessions) {
	handlerSessions.Store(h, s)
}

// startSession adds a new console session of "tester" to req, with a sign-in
// age old, for the handler h. The handler must have been built with
// signInOf and rememberSessions.
func startSession(t *testing.T, h *apiserver.Handler, req *http.Request, age time.Duration) {
	t.Helper()

	s, ok := handlerSessions.Load(h)
	if !ok {
		t.Fatal("the handler has no sessions: build it with signInOf and rememberSessions")
	}

	sessions, _ := s.(storeSessions)
	sessions.start(t, req, age)
}
