package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/firstadminsvc"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// The fixed values of the first administrator tests.
const (
	faOfferPath  = "/api/server/first-administrator"
	faRedeemPath = "/api/server/first-administrator/redeem"
	faReason     = "first administrator of the new installation"
	faAlice      = "prn-alice"
	faBob        = "prn-bob"
)

// faAuthenticator signs in the fixed tokens of the tests.
type faAuthenticator struct{}

func (faAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	p := identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: token, DisplayName: token,
		Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	switch token {
	case "boot":
		// The static bootstrap administrator: no token ID, no kind, no credential.
		p.Admin = true
	case "personal-admin":
		p.Admin = true
		p.TokenID = "tok-personal-admin"
		p.Kind = identity.KindPerson
	case "temp-admin":
		p.Admin = true
		p.TemporaryAdministrator = true
		p.TemporaryExpiresAt = time.Now().Add(time.Hour)
	case "alice", "bob":
		p.TokenID = "tok-" + token
		p.Kind = identity.KindPerson
	default:
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return p, nil
}

// faSpy counts the calls that reach the service, so a test tells the refusal
// of the handler from the refusal of a lower layer.
type faSpy struct {
	inner   apiserver.FirstAdministratorService
	mu      sync.Mutex
	offers  int
	redeems int
}

func (s *faSpy) State(ctx context.Context) (firstadmin.State, error) { return s.inner.State(ctx) }

func (s *faSpy) Offer(ctx context.Context, in firstadminsvc.OfferInput) (firstadminsvc.OfferResult, error) {
	s.mu.Lock()
	s.offers++
	s.mu.Unlock()

	return s.inner.Offer(ctx, in)
}

func (s *faSpy) Redeem(ctx context.Context, in firstadminsvc.RedeemInput) (firstadmin.Result, error) {
	s.mu.Lock()
	s.redeems++
	s.mu.Unlock()

	return s.inner.Redeem(ctx, in)
}

func (s *faSpy) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.offers + s.redeems
}

// faLog is a log sink that tests read.
type faLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *faLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p) //nolint:wrapcheck // A test sink.
}

func (l *faLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

// faFixture is a handler over a real SQLite store with the first
// administrator service, two persons (alice and bob) and no grant.
type faFixture struct {
	store    *sqlite.Store
	path     string
	sessions *session.Sessions
	handler  testHandler
	limiter  *fakeLimiter
	spy      *faSpy
	log      *faLog
}

func newFAFixture(t *testing.T) faFixture {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	st := openServerTemplateStore(t, path)
	logs := &faLog{mu: sync.Mutex{}, buf: bytes.Buffer{}}
	logger := slog.New(
		slog.NewTextHandler(logs, &slog.HandlerOptions{AddSource: false, Level: slog.LevelDebug, ReplaceAttr: nil}),
	)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	authz, err := access.NewAuthorizer(
		access.AuthorizerConfig{Store: st, Callers: st, Collections: stubCollections{}, Units: st, Principals: st},
	)
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	reach := access.ReachConfig{
		Grants: st, Roles: st, Callers: st, Principals: st, Collections: stubCollections{}, Units: st,
	}

	grants, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: authz, Reach: reach, Now: nil, NewID: nil,
		BootstrapAdmins: []string{"boot"}, Settings: nil, LimitGroups: nil,
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	principals, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      grants,
		GroupReach: grants,
		Namespaces: st,
		Tokens:     grants,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil, Store: st, Principals: st, Freshness: nil, Authorizer: authz, Reach: reach, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: nil})
	if err != nil {
		t.Fatalf("session policy service: %v", err)
	}

	sessions, err := session.NewSessions(session.SessionsConfig{Store: st, Providers: st, Inactivity: nil, Now: nil})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	svc, err := firstadminsvc.New(firstadminsvc.Config{
		Store: st, Principals: principals, TempAdmins: st, Audit: audit.ServerStore(st), Now: time.Now,
		Random: rand.Reader, GrantID: func() string { return ident.NewID(access.GrantIDPrefix) },
		Window: func(ctx context.Context) time.Duration {
			p, policyErr := policies.Policy(ctx)
			if policyErr != nil {
				return 0
			}

			return p.TokenFreshness
		},
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("first administrator service: %v", err)
	}

	spy := &faSpy{inner: svc, mu: sync.Mutex{}, offers: 0, redeems: 0}
	limiter := &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		ProxyRules: nil, Cursors: testCursors(t), LocalUsers: nil, Preferences: nil, GitHostLinks: nil,
		Settings: nil, GitHost: nil, Idempotency: nil, Workers: nil, InstallationKind: "",
		StateWriteGates: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:             nil, TempAdmins: nil, Authenticator: faAuthenticator{}, Authorizer: authz, Logger: logger,
		Namespaces: namespaces, Backups: nil, Restores: nil, Maintenance: newFakeMaintenance(maintenance.StateNormal),
		Metrics: stubMetrics{}, Version: "", Schema: stubSchema{migrations: nil}, Journal: nil, Principals: principals,
		Tokens: tokens, Access: grants, Audit: st, OnAuditFailure: nil, DeletedUnits: nil, AuditChain: nil,
		AuditSinks: nil, IPAllowlists: nil, Gates: nil, SessionPolicy: policies,
		SignIn: &apiserver.SignIn{
			Service: newTestSignInService(t, st, sessions, principals), OnDevRefused: nil, DevCode: nil,
			DevAdmin: noBootstrapAdmin, Sessions: sessions, Limiter: limiter, Stored: nil, Admin: nil,
			PasswordProviders: nil, Origins: []string{signInBase}, Providers: nil, PasswordLimiter: nil, Passkeys: nil,
			PasskeySignIn: nil, PasskeyBeginLimiter: nil, PasskeyLimiter: nil,
		},
		CheckpointEpochs: nil, DenialLimiter: nil, Instances: nil, StoreChecks: nil, Reach: reach,
		FirstAdministrator: spy,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	f := faFixture{
		store: st, path: path, sessions: sessions, handler: handler, limiter: limiter, spy: spy, log: logs,
	}

	for _, subject := range []string{"alice", "bob"} {
		f.person(t, subject)
	}

	return f
}

// person stores the live person "prn-<subject>".
func (f faFixture) person(t *testing.T, subject string) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-" + subject, Subject: subject,
		DisplayName: subject, Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	if err := f.store.CreatePrincipal(t.Context(), p, storetest.ServerChange(at, principal.Ref(p.ID))); err != nil {
		t.Fatalf("seed person %s: %v", subject, err)
	}
}

// faCaller is how a request signs in: a token, or a console session.
type faCaller struct {
	headers map[string]string
	token   string
	session string
	age     time.Duration
	noCSRF  bool
}

func asToken(token string) faCaller {
	return faCaller{headers: nil, token: token, session: "", age: 0, noCSRF: false}
}

// asSession is the console session of the person with the subject, with a
// sign-in now.
func asSession(subject string) faCaller {
	return faCaller{headers: nil, token: "", session: subject, age: 0, noCSRF: false}
}

// signedInAgo returns the caller with a sign-in that is age old.
func (c faCaller) signedInAgo(age time.Duration) faCaller {
	c.age = -age

	return c
}

// withoutCSRF returns the caller with no CSRF value.
func (c faCaller) withoutCSRF() faCaller {
	c.noCSRF = true

	return c
}

// with returns the caller with the extra request headers.
func (c faCaller) with(headers map[string]string) faCaller {
	c.headers = headers

	return c
}

// sessionOf starts a console session of the person "prn-<subject>" with a
// sign-in at authTime, and returns its cookie and CSRF value.
func (f faFixture) sessionOf(t *testing.T, subject string, authTime time.Time) (*http.Cookie, string) {
	t.Helper()

	return accessFixture{store: f.store, path: f.path, sessions: f.sessions, handler: f.handler, passwords: f.limiter}.
		sessionOf(t, subject, authTime)
}

// send sends one request as who.
func (f faFixture) send(t *testing.T, method, target, body string, who faCaller) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	switch {
	case who.token != "":
		req.Header.Set("Authorization", "Bearer "+who.token)
	case who.session != "":
		cookie, csrf := f.sessionOf(t, who.session, time.Now().Add(who.age))

		req.AddCookie(cookie)
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		if !who.noCSRF {
			req.Header.Set(apiserver.CSRFHeader, csrf)
		}
	default:
		// No credential: the request is sent as it is.
	}

	for k, v := range who.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

// offerDoc is the answer of the offer on the wire.
type offerDoc struct {
	Principal string `json:"principal"`
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
}

type faErrorBody struct {
	ErrorCode      string `json:"errorCode"`
	Message        string `json:"message"`
	Reauthenticate string `json:"reauthenticate"`
}

// want fails unless rec has status and, when code is not empty, the error code.
func faWant(t *testing.T, what string, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("%s: status = %d, want %d (%s)", what, rec.Code, status, rec.Body.String())
	}

	if code == "" {
		return
	}

	var body faErrorBody
	decodeInto(t, rec.Body.String(), &body)

	if body.ErrorCode != code {
		t.Fatalf("%s: errorCode = %q, want %q (%s)", what, body.ErrorCode, code, rec.Body.String())
	}
}

// offer asks for a code for person as the bootstrap administrator and returns the code.
func (f faFixture) offer(t *testing.T) string {
	t.Helper()

	rec := f.send(t, http.MethodPost, faOfferPath, faOfferBody(faAlice), asToken("boot"))
	faWant(t, "offer", rec, http.StatusCreated, "")

	var got offerDoc

	decodeInto(t, rec.Body.String(), &got)

	if got.Principal != faAlice || len(got.Code) != firstadmin.CodeLength || got.ExpiresAt == "" {
		t.Fatalf("offer answer = %s", rec.Body.String())
	}

	return got.Code
}

func faOfferBody(person string) string {
	return fmt.Sprintf(`{"principal":%q,"reason":%q}`, person, faReason)
}

func faCodeBody(code string) string { return fmt.Sprintf(`{"code":%q}`, code) }

// state reads the marker and the open offer.
// faState is the answer of the read: the marker is set, an offer is open.
type faState struct {
	made    bool
	offered bool
}

func (f faFixture) state(t *testing.T) faState {
	t.Helper()

	rec := f.send(t, http.MethodGet, faOfferPath, "", asToken("boot"))
	faWant(t, "read state", rec, http.StatusOK, "")

	var got struct {
		Offer json.RawMessage `json:"offer"`
		Made  bool            `json:"made"`
	}

	decodeInto(t, rec.Body.String(), &got)

	return faState{made: got.Made, offered: string(got.Offer) != "null"}
}

// setWindow stores the token freshness window of the session policy.
func (f faFixture) setWindow(t *testing.T, window time.Duration) {
	t.Helper()

	_, err := f.store.UpdateSessionPolicy(t.Context(), func(old session.Policy) (session.Policy, audit.Entry, error) {
		old.TokenFreshness, old.TokenFreshnessDefault, old.UpdatedAt = window, false, time.Now()

		return old, policyChangeEntry(time.Now()), nil
	})
	if err != nil {
		t.Fatalf("set the window to %s: %v", window, err)
	}
}

// serverEntries returns the audit entries of the scope @server with action.
func (f faFixture) serverEntries(t *testing.T, action string) []audit.Entry {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if action == "" || listing.Entries[i].Action == action {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

// wantNoGrantOf fails when the person holds a grant: nothing was made.
func (f faFixture) wantOpen(t *testing.T, what string) {
	t.Helper()

	if f.state(t).made {
		t.Fatalf("%s: the marker is stored, want none", what)
	}
}

// faReq is one request of a test: the method, the target, the body and the
// caller. The text "%CODE%" in the target, the body or a header value is the
// code of the offer that the test made first.
type faReq struct {
	method string
	target string
	body   string
	who    faCaller
}

// faWanted is the answer that a row expects: the status, the error code, the
// rule that the failure entry names, the number of calls that reach the
// service and the units of the failure budget that the request spends.
type faWanted struct {
	status       int
	code         string
	rule         string
	serviceCalls int
	failures     int
}

// faRow is one hostile row of the handler layer.
type faRow struct {
	name string
	req  faReq
	want faWanted
	// offered makes an offer for alice before the request.
	offered bool
	// windowZero sets the freshness window of the session policy to 0.
	windowZero bool
	// budgetSpent makes the failure limiter of the address refuse.
	budgetSpent bool
}

const (
	faNoCode  = ""
	faNoRule  = ""
	faNoCalls = 0
	faNoCost  = 0
)

// faWrongCode is a code of the right form that no offer made.
var faWrongCode = strings.Repeat("A", firstadmin.CodeLength) //nolint:gochecknoglobals // A frozen test value.

func faOfferReq(body string, who faCaller) faReq {
	return faReq{method: http.MethodPost, target: faOfferPath, body: body, who: who}
}

func faRedeemReq(body string, who faCaller) faReq {
	return faReq{method: http.MethodPost, target: faRedeemPath, body: body, who: who}
}

// faOfferRows are the hostile rows of the offer.
func faOfferRows() []faRow {
	body := faOfferBody(faAlice)
	boot := asToken("boot")

	return []faRow{
		{
			name: "offer with an unknown field", offered: false, windowZero: false, budgetSpent: false,
			req:  faOfferReq(`{"principal":"prn-alice","reason":"r","admin":true}`, boot),
			want: faWanted{http.StatusBadRequest, "request.body-invalid", "body-invalid", faNoCalls, faNoCost},
		},
		{
			name: "offer with a body over the limit", offered: false, windowZero: false, budgetSpent: false,
			req: faOfferReq(`{"principal":"prn-alice","reason":"`+strings.Repeat("a", 5000)+`"}`, boot),
			want: faWanted{
				http.StatusRequestEntityTooLarge, "request.body-too-large", "body-too-large", faNoCalls, faNoCost,
			},
		},
		{
			name: "offer with a query value", offered: false, windowZero: false, budgetSpent: false,
			req: faReq{
				method: http.MethodPost, target: faOfferPath + "?principal=prn-alice", body: body, who: boot,
			},
			want: faWanted{http.StatusBadRequest, "request.invalid-query", "query-not-empty", faNoCalls, faNoCost},
		},
		{
			name: "offer with an Idempotency-Key", offered: false, windowZero: false, budgetSpent: false,
			req: faOfferReq(body, boot.with(map[string]string{"Idempotency-Key": "k1"})),
			want: faWanted{
				http.StatusBadRequest, "request.idempotency-key-unsupported", "idempotency-key", faNoCalls, faNoCost,
			},
		},
		{
			name: "offer with a non-admin token", offered: false, windowZero: false, budgetSpent: false,
			req:  faOfferReq(body, asToken("bob")),
			want: faWanted{http.StatusForbidden, "access.denied", faNoRule, faNoCalls, faNoCost},
		},
		{
			name: "offer with a personal token with Admin", offered: false, windowZero: false, budgetSpent: false,
			req:  faOfferReq(body, asToken("personal-admin")),
			want: faWanted{http.StatusForbidden, "access.denied", "caller-not-bootstrap", faNoCalls, faNoCost},
		},
		{
			name: "offer with a temporary administrator", offered: false, windowZero: false, budgetSpent: false,
			req:  faOfferReq(body, asToken("temp-admin")),
			want: faWanted{http.StatusForbidden, "access.denied", "caller-not-bootstrap", faNoCalls, faNoCost},
		},
		{
			name: "offer with a console session", offered: false, windowZero: false, budgetSpent: false,
			req:  faOfferReq(body, asSession("alice")),
			want: faWanted{http.StatusForbidden, "access.denied", faNoRule, faNoCalls, faNoCost},
		},
	}
}

// faRedeemRows are the hostile rows of the redeem.
func faRedeemRows() []faRow {
	code := `{"code":"%CODE%"}`
	alice := asSession("alice")
	freshStep := "mfa.fresh-step-required"
	invalid := "first-administrator.code-invalid"

	return []faRow{
		{
			name: "redeem with an access token", offered: true, windowZero: false, budgetSpent: false,
			req:  faRedeemReq(code, asToken("alice")),
			want: faWanted{http.StatusForbidden, freshStep, "no-console-session", faNoCalls, faNoCost},
		},
		{
			name: "redeem with no CSRF value", offered: true, windowZero: false, budgetSpent: false,
			req:  faRedeemReq(code, alice.withoutCSRF()),
			want: faWanted{http.StatusForbidden, faNoCode, faNoRule, faNoCalls, faNoCost},
		},
		{
			name: "redeem with a stale sign-in", offered: true, windowZero: false, budgetSpent: false,
			req:  faRedeemReq(code, alice.signedInAgo(24*time.Hour)),
			want: faWanted{http.StatusForbidden, freshStep, "stale-sign-in", faNoCalls, faNoCost},
		},
		{
			name: "redeem with a window of 0", offered: true, windowZero: true, budgetSpent: false,
			req:  faRedeemReq(code, alice),
			want: faWanted{http.StatusForbidden, freshStep, "stale-sign-in", faNoCalls, faNoCost},
		},
		{
			name: "redeem with an unknown field", offered: true, windowZero: false, budgetSpent: false,
			req:  faRedeemReq(`{"code":"%CODE%","principal":"prn-bob"}`, alice),
			want: faWanted{http.StatusBadRequest, "request.body-invalid", "body-invalid", faNoCalls, faNoCost},
		},
		{
			name:        "redeem with a body over the limit",
			offered:     true,
			windowZero:  false,
			budgetSpent: false,
			req:         faRedeemReq(`{"code":"`+strings.Repeat("a", 5000)+`"}`, alice),
			want: faWanted{
				http.StatusRequestEntityTooLarge,
				"request.body-too-large",
				"body-too-large",
				faNoCalls,
				faNoCost,
			},
		},
		{
			name: "redeem with the code in the query", offered: true, windowZero: false, budgetSpent: false,
			req:  faReq{method: http.MethodPost, target: faRedeemPath + "?code=%CODE%", body: code, who: alice},
			want: faWanted{http.StatusBadRequest, "request.invalid-query", "query-not-empty", faNoCalls, faNoCost},
		},
		{
			name: "redeem with an Idempotency-Key", offered: true, windowZero: false, budgetSpent: false,
			req: faRedeemReq(code, alice.with(map[string]string{"Idempotency-Key": "k1"})),
			want: faWanted{
				http.StatusBadRequest, "request.idempotency-key-unsupported", "idempotency-key", faNoCalls, faNoCost,
			},
		},
		{
			name: "redeem with the code only in a header", offered: true, windowZero: false, budgetSpent: false,
			req: faRedeemReq(faCodeBody(faWrongCode), alice.with(map[string]string{
				"X-Dustpan-Code": "%CODE%", "Authorization-Code": "%CODE%",
			})),
			want: faWanted{http.StatusBadRequest, invalid, "code-invalid", 1, 1},
		},
		{
			name: "redeem with a wrong code spends the failure budget", offered: true, windowZero: false,
			budgetSpent: false,
			req:         faRedeemReq(faCodeBody(faWrongCode), alice),
			want:        faWanted{http.StatusBadRequest, invalid, "code-invalid", 1, 1},
		},
		{
			name: "redeem with a code of another form spends the failure budget", offered: true, windowZero: false,
			budgetSpent: false,
			req:         faRedeemReq(faCodeBody("short"), alice),
			want:        faWanted{http.StatusBadRequest, invalid, "code-form", 1, 1},
		},
		{
			name: "redeem of the code of another person", offered: true, windowZero: false, budgetSpent: false,
			req:  faRedeemReq(code, asSession("bob")),
			want: faWanted{http.StatusBadRequest, invalid, "code-invalid", 1, 1},
		},
		{
			name: "redeem with the failure budget spent", offered: true, windowZero: false, budgetSpent: true,
			req:  faRedeemReq(code, alice),
			want: faWanted{http.StatusTooManyRequests, faNoCode, faNoRule, faNoCalls, faNoCost},
		},
	}
}

// faSubstitute replaces the text "%CODE%" in the request with code.
func faSubstitute(req faReq, code string) faReq {
	req.target = strings.ReplaceAll(req.target, "%CODE%", code)
	req.body = strings.ReplaceAll(req.body, "%CODE%", code)

	headers := make(map[string]string, len(req.who.headers))
	for k, v := range req.who.headers {
		headers[k] = strings.ReplaceAll(v, "%CODE%", code)
	}

	req.who.headers = headers

	return req
}

// TestHandlerHostileRows sends hostile input to the handler layer. Each row
// that the handler refuses itself shows that no call reached the service, so
// the row fails when the guard of the handler is removed, even though the
// service and the store hold the same rule.
func TestHandlerHostileRows(t *testing.T) {
	t.Parallel()

	for _, tc := range append(faOfferRows(), faRedeemRows()...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runFAHostileRow(t, &tc)
		})
	}
}

// runFAHostileRow sends the request of one row to a new fixture and checks the
// answer, the calls that reached the service, the failure budget and the audit
// entry of the refusal.
func runFAHostileRow(t *testing.T, tc *faRow) {
	t.Helper()

	f := newFAFixture(t)

	code := ""
	if tc.offered {
		code = f.offer(t)
	}

	if tc.windowZero {
		f.setWindow(t, 0)
	}

	if tc.budgetSpent {
		f.limiter.refuseFromNow()
	}

	req := faSubstitute(tc.req, code)
	callsBefore := f.spy.calls()
	budgetBefore := f.limiter.count()

	rec := f.send(t, req.method, req.target, req.body, req.who)
	faWant(t, tc.name, rec, tc.want.status, tc.want.code)

	if got := f.spy.calls() - callsBefore; got != tc.want.serviceCalls {
		t.Fatalf("service calls = %d, want %d: the handler must refuse before the service", got, tc.want.serviceCalls)
	}

	if got := f.limiter.count() - budgetBefore; got != tc.want.failures {
		t.Fatalf("failure budget spent = %d, want %d", got, tc.want.failures)
	}

	f.wantOpen(t, tc.name)

	if tc.want.rule != "" {
		f.wantFailureRule(t, tc.name, req.target, tc.want.rule)
	}

	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}

// TestHandlerRedeemMakesTheFirstAdministrator is the answer of the route in
// the good case: the answer, the grant, the marker and the closed route.
func TestHandlerRedeemMakesTheFirstAdministrator(t *testing.T) {
	t.Parallel()

	f := newFAFixture(t)
	code := f.offer(t)

	rec := f.send(t, http.MethodPost, faRedeemPath, faCodeBody(code), asSession("alice"))
	faWant(t, "redeem", rec, http.StatusOK, "")

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("redeem Cache-Control = %q, want no-store", got)
	}

	var got struct {
		Principal string `json:"principal"`
		Role      string `json:"role"`
		Grant     string `json:"grant"`
		MadeAt    string `json:"madeAt"`
	}

	decodeInto(t, rec.Body.String(), &got)

	if got.Principal != faAlice || got.Role != access.RoleSiteAdministrator || got.Grant == "" || got.MadeAt == "" {
		t.Fatalf("redeem answer = %s", rec.Body.String())
	}

	if got := f.state(t); !got.made || got.offered {
		t.Fatalf("state made = %v, offer = %v, want made and no offer", got.made, got.offered)
	}

	// The route is closed for each caller.
	again := f.send(t, http.MethodPost, faOfferPath, faOfferBody(faBob), asToken("boot"))
	faWant(t, "offer after the marker", again, http.StatusConflict, "first-administrator.already-made")

	f.wantEntries(t)
}

// wantEntries fails unless the audit log holds the offer entry and one
// successful redeem entry with the reason of the offer.
func (f faFixture) wantEntries(t *testing.T) {
	t.Helper()

	offers := f.serverEntries(t, string(identity.ActionServerOfferFirstAdministrator))
	redeems := f.serverEntries(t, string(identity.ActionCallerRedeemFirstAdministrator))

	succeeded := 0

	for i := range redeems {
		if redeems[i].Outcome != audit.OutcomeSuccess {
			continue
		}

		succeeded++

		if redeems[i].Reason != faReason || redeems[i].Subject != "alice" {
			t.Fatalf("redeem entry reason = %q subject = %q", redeems[i].Reason, redeems[i].Subject)
		}
	}

	if len(offers) == 0 || succeeded != 1 {
		t.Fatalf("offer entries = %d, successful redeem entries = %d, want some and 1", len(offers), succeeded)
	}
}

// TestRedeemIgnoresFreshAuthEnabled: the redeem reads only the window of the
// session policy. A stale sign-in is refused and a fresh sign-in is accepted
// whatever the installation settings say, and the source of the route names no
// setting.
func TestRedeemIgnoresFreshAuthEnabled(t *testing.T) {
	t.Parallel()

	f := newFAFixture(t)
	code := f.offer(t)

	stale := f.send(t, http.MethodPost, faRedeemPath, faCodeBody(code), asSession("alice").signedInAgo(24*time.Hour))
	faWant(t, "stale sign-in", stale, http.StatusForbidden, "mfa.fresh-step-required")

	fresh := f.send(t, http.MethodPost, faRedeemPath, faCodeBody(code), asSession("alice"))
	faWant(t, "fresh sign-in", fresh, http.StatusOK, "")

	source, err := packageSources.ReadFile("firstadmin.go")
	if err != nil {
		t.Fatalf("read the source of the route: %v", err)
	}

	for _, banned := range []string{"freshAuthEnabled", "h.settings", "FreshAuthEnabled"} {
		if bytes.Contains(source, []byte(banned)) {
			t.Fatalf("firstadmin.go names %q: the redeem must not ask the setting", banned)
		}
	}
}

// TestFirstAdministratorRoutesAreNotIdempotent: no route takes an
// Idempotency-Key, so no stored answer can hold the code.
func TestFirstAdministratorRoutesAreNotIdempotent(t *testing.T) {
	t.Parallel()

	f := newFAFixture(t)

	// The description covers the routes of one prefix only, so the test reads
	// the route table: three routes, and each refuses the key.
	seen := 0

	for _, route := range f.handler.Routes() {
		if strings.HasPrefix(route.Path, faOfferPath) {
			seen++
		}
	}

	if seen != 3 {
		t.Fatalf("routes of the first administrator = %d, want 3", seen)
	}

	key := map[string]string{"Idempotency-Key": "same-key"}

	for range 2 {
		rec := f.send(t, http.MethodPost, faOfferPath, faOfferBody(faAlice), asToken("boot").with(key))
		faWant(t, "offer with a key", rec, http.StatusBadRequest, "request.idempotency-key-unsupported")
	}

	rec := f.send(t, http.MethodPost, faRedeemPath, faCodeBody(faWrongCode), asSession("alice").with(key))
	faWant(t, "redeem with a key", rec, http.StatusBadRequest, "request.idempotency-key-unsupported")

	// Two offers make two codes: nothing is replayed.
	if one, two := f.offer(t), f.offer(t); one == two {
		t.Fatal("two offers gave the same code")
	}

	var records int

	row := f.sqlDB(t).QueryRowContext(t.Context(), "SELECT COUNT(*) FROM idempotency_records")
	if err := row.Scan(&records); err != nil {
		t.Fatalf("count the replay records: %v", err)
	}

	if records != 0 {
		t.Fatalf("replay records = %d, want 0", records)
	}
}

// TestCodeNeverLeaks: the code and its digest are in no log line, no audit
// entry, no error text and no stored row, except the digest in its own table
// until the code is spent. Every answer of the routes has Cache-Control:
// no-store.
func TestCodeNeverLeaks(t *testing.T) {
	t.Parallel()

	f := newFAFixture(t)

	rec := f.send(t, http.MethodPost, faOfferPath, faOfferBody(faAlice), asToken("boot"))
	faWant(t, "offer", rec, http.StatusCreated, "")

	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("offer Cache-Control = %q, want no-store", got)
	}

	var offered offerDoc

	decodeInto(t, rec.Body.String(), &offered)

	code := offered.Code
	digest := firstadmin.Code(code).Digest()

	texts := f.refusalTexts(t, code)
	texts = append(texts,
		f.send(t, http.MethodGet, faOfferPath, "", asToken("boot")).Body.String(),
		f.send(t, http.MethodGet, "/api/server/metrics", "", asToken("boot")).Body.String(),
	)

	f.wantCodeNowhere(t, "before the redeem", code)
	f.wantDigestOnlyInCodeTable(t, "before the redeem", digest)

	good := f.send(t, http.MethodPost, faRedeemPath, faCodeBody(code), asSession("alice"))
	faWant(t, "redeem", good, http.StatusOK, "")

	texts = append(texts, good.Body.String(), f.log.text())

	f.wantCodeNowhere(t, "after the redeem", code)
	f.wantDigestNowhere(t, "after the redeem", digest)

	for _, text := range texts {
		if strings.Contains(text, code) || strings.Contains(text, digest) {
			t.Fatalf("an answer or the log holds the code or its digest: %s", text)
		}
	}

	f.wantEntriesClean(t, code, digest)
}

// refusalTexts sends the refusals that handle the code and returns the text of
// each answer. Each answer has Cache-Control: no-store.
func (f faFixture) refusalTexts(t *testing.T, code string) []string {
	t.Helper()

	reqs := []struct {
		who  faCaller
		body string
	}{
		{asSession("bob"), faCodeBody(code)},
		{asSession("alice"), faCodeBody(faWrongCode)},
		{asSession("alice").signedInAgo(24 * time.Hour), faCodeBody(code)},
		{asToken("alice"), faCodeBody(code)},
	}

	texts := make([]string, 0, len(reqs)+3)

	for _, c := range reqs {
		r := f.send(t, http.MethodPost, faRedeemPath, c.body, c.who)
		if r.Code < http.StatusBadRequest {
			t.Fatalf("a refusal answered %d", r.Code)
		}

		if r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("refusal Cache-Control = %q", r.Header().Get("Cache-Control"))
		}

		texts = append(texts, r.Body.String())
	}

	return texts
}

// wantEntriesClean fails when an audit entry holds the code or its digest.
func (f faFixture) wantEntriesClean(t *testing.T, code, digest string) {
	t.Helper()

	entries := f.serverEntries(t, "")

	for i := range entries {
		text := fmt.Sprintf("%+v", entries[i])

		if strings.Contains(text, code) || strings.Contains(text, digest) {
			t.Fatalf("an audit entry holds the code or its digest: %s", text)
		}
	}
}

// wantCodeNowhere fails when a table holds the code.
func (f faFixture) wantCodeNowhere(t *testing.T, when, code string) {
	t.Helper()

	for name, text := range f.tableTexts(t) {
		if strings.Contains(text, code) {
			t.Fatalf("%s: table %s holds the code", when, name)
		}
	}
}

// wantDigestOnlyInCodeTable fails unless only the code table holds the digest.
func (f faFixture) wantDigestOnlyInCodeTable(t *testing.T, when, digest string) {
	t.Helper()

	var holders []string

	for name, text := range f.tableTexts(t) {
		if strings.Contains(text, digest) {
			holders = append(holders, name)
		}
	}

	if len(holders) != 1 || holders[0] != "first_administrator_codes" {
		t.Fatalf("%s: the digest is in the tables %v, want only first_administrator_codes", when, holders)
	}
}

// wantDigestNowhere fails when a table holds the digest.
func (f faFixture) wantDigestNowhere(t *testing.T, when, digest string) {
	t.Helper()

	for name, text := range f.tableTexts(t) {
		if strings.Contains(text, digest) {
			t.Fatalf("%s: table %s holds the digest", when, name)
		}
	}
}

// tableTexts returns the text of every row of every table, by table name.
func (f faFixture) tableTexts(t *testing.T) map[string]string {
	t.Helper()

	conn := f.sqlDB(t)

	tables, err := conn.QueryContext(t.Context(), "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}

	defer func() {
		if closeErr := tables.Close(); closeErr != nil {
			t.Errorf("close tables: %v", closeErr)
		}
	}()

	var names []string

	for tables.Next() {
		var name string
		if scanErr := tables.Scan(&name); scanErr != nil {
			t.Fatalf("scan table name: %v", scanErr)
		}

		names = append(names, name)
	}

	if err = tables.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}

	out := make(map[string]string, len(names))
	for _, name := range names {
		out[name] = f.dumpTable(t, conn, name)
	}

	return out
}

// dumpTable returns every value of every row of table as text.
func (f faFixture) dumpTable(t *testing.T, conn *sql.DB, table string) string {
	t.Helper()

	// The name comes from sqlite_master of the database of this test, and the
	// quote is removed from it.
	query := `SELECT * FROM "` + strings.ReplaceAll(table, `"`, "") + `"` //nolint:gosec,unqueryvet // See above.

	rows, err := conn.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatalf("read table %s: %v", table, err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows: %v", closeErr)
		}
	}()

	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}

	var out strings.Builder

	for rows.Next() {
		out.WriteString(scanRowText(t, rows, len(columns)))
	}

	if err = rows.Err(); err != nil {
		t.Fatalf("read rows of %s: %v", table, err)
	}

	return out.String()
}

// scanRowText returns the values of the current row as one line of text.
func scanRowText(t *testing.T, rows *sql.Rows, width int) string {
	t.Helper()

	values := make([]any, width)

	pointers := make([]any, width)
	for i := range values {
		pointers[i] = &values[i]
	}

	if err := rows.Scan(pointers...); err != nil {
		t.Fatalf("scan a row: %v", err)
	}

	var out strings.Builder

	for _, v := range values {
		if b, ok := v.([]byte); ok {
			out.Write(b)
		} else {
			fmt.Fprint(&out, v)
		}

		out.WriteByte('|')
	}

	out.WriteByte('\n')

	return out.String()
}

// sqlDB opens a second connection to the database file of the fixture.
func (f faFixture) sqlDB(t *testing.T) *sql.DB {
	t.Helper()

	conn, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	})

	return conn
}

// wantFailureRule fails unless the audit log holds a failure entry of the
// action of target that names rule.
func (f faFixture) wantFailureRule(t *testing.T, what, target, rule string) {
	t.Helper()

	action := string(identity.ActionServerOfferFirstAdministrator)
	if strings.HasPrefix(target, faRedeemPath) {
		action = string(identity.ActionCallerRedeemFirstAdministrator)
	}

	var seen []string

	entries := f.serverEntries(t, action)

	for i := range entries {
		if entries[i].Outcome != audit.OutcomeFailure {
			continue
		}

		for _, c := range entries[i].Changes {
			if c.Key != "rule" {
				continue
			}

			if c.New == rule {
				return
			}

			seen = append(seen, c.New)
		}
	}

	t.Fatalf("%s: no failure entry of %s names the rule %q (rules: %v)", what, action, rule, seen)
}

// newRouteFirstAdmin builds the first administrator service over st for the
// generic route tests.
func newRouteFirstAdmin(t *testing.T, st *sqlite.Store, principals *principal.Service) *firstadminsvc.Service {
	t.Helper()

	svc, err := firstadminsvc.New(firstadminsvc.Config{
		Store: st, Principals: principals, TempAdmins: st, Audit: audit.ServerStore(st), Now: time.Now,
		Random: rand.Reader, GrantID: func() string { return ident.NewID(access.GrantIDPrefix) },
		Window: func(context.Context) time.Duration { return session.DefaultTokenFreshness },
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("first administrator service: %v", err)
	}

	return svc
}

// refuseFromNow makes the limiter refuse each later request, as an address
// with a spent failure budget does.
func (l *fakeLimiter) refuseFromNow() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refuse = true
}
