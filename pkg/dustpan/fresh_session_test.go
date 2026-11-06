package dustpan_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/oauth/oauthtest"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// consoleBackend is the store surface that a console session of a test needs:
// sessions, one person and one grant.
type consoleBackend interface {
	session.Store
	session.ProviderStore
	principal.Store
	access.Store
}

const (
	// consoleSubject is the person of the console session of the tests.
	consoleSubject = "admin"
	consolePerson  = "prn-admin"
	// consoleSetupActor is the actor of the audit entries that the setup of the
	// console session writes, so a test can leave them out.
	consoleSetupActor = "console-setup"
)

// signInOptions makes a server accept console sessions: it has a public URL
// and one sign-in provider, which a test provider answers.
func signInOptions(t *testing.T, st consoleBackend) []dustpan.Option {
	t.Helper()

	const publicURL = "https://dustpan.example"

	// A server that starts in maintenance mode cannot record the issuer of its
	// provider, so the issuer is recorded now. One test provider serves all
	// servers of a store, because a provider ID keeps its first issuer.
	idp := providerOf(t, st, publicURL)

	bound := []session.Provider{{ID: "corp", Issuer: idp.Issuer(), UserinfoURL: "", SubjectClaim: ""}}

	err := session.BindProviders(t.Context(), st, bound, time.Now())
	if err != nil {
		t.Fatalf("record the issuer: %v", err)
	}

	return []dustpan.Option{
		dustpan.WithPublicURL(publicURL),
		dustpan.WithOIDCProvider(dustpan.OIDCProvider{
			HTTPClient: idp.Client(), ID: "corp", DisplayName: "Corp", Issuer: idp.Issuer(), ClientID: "dustpan",
			ClientSecret: "client-secret-for-tests", Scopes: []string{"profile", "email"}, AutoCreate: false,
		}),
	}
}

// providers holds the test provider of each store.
//
//nolint:gochecknoglobals // A test-only registry of stores.
var providers sync.Map

// providerOf returns the test provider of st and makes it on the first call.
func providerOf(t *testing.T, st consoleBackend, publicURL string) *oauthtest.Provider {
	t.Helper()

	if found, ok := providers.Load(st); ok {
		if idp, isIDP := found.(*oauthtest.Provider); isIDP {
			return idp
		}
	}

	idp := oauthtest.New(t, oauthtest.Config{
		Now: time.Now, ClientID: "dustpan", ClientSecret: "client-secret-for-tests",
		RedirectURI: publicURL + "/api/server/sign-in/corp/callback", Algorithm: oauthtest.RS256,
		User: oauthtest.User{
			Subject: "248289761001", Name: "Alice Doe", PreferredUsername: "alice", Email: "alice@example.com",
		},
		Mode: oauthtest.ModeOIDC,
		Faults: oauthtest.Faults{
			WrongIssuer: false, WrongAudience: false, WrongNonce: false, Expired: false, BadSignature: false,
			AlgNone: false, UnknownKey: false, SkipPKCE: false, ExtraAudience: false, AuthorizedParty: "",
			IssuedIn: 0, IgnoreMaxAge: false, TokenType: "", UserinfoStatus: 0, UserinfoBody: "",
			UserinfoType: "", UserinfoRedirect: "", TokenRedirect: "", AuthTime: time.Time{},
		},
		TLS: true,
	})

	providers.Store(st, idp)

	return idp
}

// consoles maps the address of a test server to the function that adds the
// console session of a site administrator to a request.
//
//nolint:gochecknoglobals // A test-only registry: the servers have no field for it.
var consoles sync.Map

// consoleLogin is the credential of one console session: its cookie and CSRF
// value.
type consoleLogin struct {
	cookie *http.Cookie
	csrf   string
}

// logins holds the console session of each store. A server in maintenance
// mode refuses the write that starts a session, so the session starts with the
// first server of a store, before the maintenance mode starts, and later
// servers of the same store reuse it.
//
//nolint:gochecknoglobals // A test-only registry of stores.
var logins sync.Map

// registerConsole makes the requests of the helpers of these tests to addr
// use a console session for the changes that need a fresh authentication. st
// is the store of the server.
func registerConsole(t *testing.T, addr string, st consoleBackend) {
	t.Helper()

	login := loginFor(t, st, consolePerson, nil)

	consoles.Store(addr, func(_ *testing.T, req *http.Request) {
		req.AddCookie(login.cookie)
		req.Header.Set(dustpanCSRFHeader, login.csrf)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Del("Authorization")
	})
}

// loginKey names the console session of one person in one store.
type loginKey struct {
	st     consoleBackend
	person string
}

// loginFor starts the console session of the person in st on the first call
// and returns it. The person consolePerson gets the site administrator role.
// now is the clock of the server, or nil for the system clock.
func loginFor(t *testing.T, st consoleBackend, person string, now func() time.Time) consoleLogin {
	t.Helper()

	key := loginKey{st: st, person: person}

	if found, ok := logins.Load(key); ok {
		if login, isLogin := found.(consoleLogin); isLogin {
			return login
		}
	}

	sessions, err := session.NewSessions(session.SessionsConfig{Store: st, Providers: nil, Inactivity: nil, Now: now})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	if person == consolePerson {
		seedConsoleAdmin(t, st)
	}

	at := sessions.Now()
	entry := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: consoleSetupActor, IDPSubject: "", TokenID: "",
		Action: "session:start", Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelConsole, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	_, secret, err := sessions.Start(t.Context(), entry, session.Start{
		AuthTime: at, PrincipalID: person, Provider: "corp", SourceIP: "192.0.2.1",
		UserAgent: "test", Generation: 0, Stored: false, Alert: nil,
	})
	if err != nil {
		t.Fatalf("start the console session: %v", err)
	}

	//nolint:gosec // G124: a request cookie of a test.
	login := consoleLogin{
		cookie: &http.Cookie{Name: dustpanSessionCookie, Value: secret.Reveal()},
		csrf:   session.CSRFValue(secret),
	}
	logins.Store(key, login)

	return login
}

// serverLogin is the store and the clock of a server that a test serves
// through its handler.
type serverLogin struct {
	st  consoleBackend
	now func() time.Time
}

// handlers maps a server to its serverLogin.
//
//nolint:gochecknoglobals // A test-only registry of servers.
var handlers sync.Map

// registerServer records the store and the clock of srv, so addConsole can
// start a console session that the server accepts.
func registerServer(srv *dustpan.Server, st consoleBackend, now func() time.Time) {
	handlers.Store(srv, serverLogin{st: st, now: now})
}

// addConsole replaces the access token of req with a console session of the
// person. The server must be one that registerServer recorded.
func addConsole(t *testing.T, srv *dustpan.Server, req *http.Request, person string) {
	t.Helper()

	found, ok := handlers.Load(srv)
	if !ok {
		t.Fatal("the server has no console: call registerServer")
	}

	sl, isLogin := found.(serverLogin)
	if !isLogin {
		t.Fatalf("the console of the server has the type %T", found)
	}

	login := loginFor(t, sl.st, person, sl.now)

	req.AddCookie(login.cookie)
	req.Header.Set(dustpanCSRFHeader, login.csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Del("Authorization")
}

// Names of the cookie and of the header of a console session.
const (
	dustpanSessionCookie = "__Host-dustpan_session"
	dustpanCSRFHeader    = "X-Dustpan-Csrf"
)

// seedConsoleAdmin makes the person of the console session and gives it the
// site administrator role. A second call for the same store finds the person.
func seedConsoleAdmin(t *testing.T, st consoleBackend) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	entry := audit.Entry{
		At: at, Namespace: audit.ServerScope, Subject: consoleSetupActor, IDPSubject: "", TokenID: "",
		Action: "server:manage-principals", Object: "", Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	person := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: consolePerson, Subject: consoleSubject,
		DisplayName: consoleSubject, Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	entry.Object = principal.Ref(person.ID)

	if err := st.CreatePrincipal(t.Context(), person, entry); err != nil {
		t.Fatalf("make the console person: %v", err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-fresh-console",
		Role: access.RoleSiteAdministrator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: person.ID},
	}

	entry.Object = access.GrantRef(grant)

	fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

	if err := st.CreateGrant(t.Context(), grant, fence, entry, nil); err != nil {
		t.Fatalf("make the console grant: %v", err)
	}
}

// needsConsole reports whether method on path is a change that needs a fresh
// authentication.
func needsConsole(method, path string) bool {
	switch method {
	case http.MethodPost:
		return path == "/api/server/audit/chain"
	case http.MethodPut, http.MethodDelete:
		return len(path) > len("/ip-allowlist") && path[len(path)-len("/ip-allowlist"):] == "/ip-allowlist"
	default:
		return false
	}
}

// authorize adds the credential to req: the console session of the server at
// the address of the request, for a change that needs a fresh authentication
// when the server has one, else the access token.
func authorize(t *testing.T, req *http.Request, token string) {
	t.Helper()

	if needsConsole(req.Method, req.URL.Path) {
		if add, ok := consoles.Load(req.URL.Host); ok {
			if fn, isFn := add.(func(*testing.T, *http.Request)); isFn {
				fn(t, req)

				return
			}
		}
	}

	req.Header.Set("Authorization", "Bearer "+token)
}
