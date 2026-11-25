package trustrules_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/trustrules"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

const (
	adminToken = "admin-token"
	// storedToken signs in ada as with a stored access token.
	storedToken = "stored-token"
	// personToken signs in tm, and directToken signs in direct: two
	// persons with no administrator flag, for the reach cases.
	personToken      = "person-token"
	directToken      = "direct-token"
	installationName = "eu-west"
	publicURL        = "https://dustpan.example.com"
	// rulesURL is the path of the trust rules of the namespace acme.
	rulesURL = "/api/dustpan/v1/namespaces/acme/trust-rules"
)

// tokenAuthenticator signs in the administrator ada: with no token ID for
// adminToken, as a bootstrap token does, and with a token ID for
// storedToken, as a stored access token does. It signs in the persons tm
// and direct for the reach cases.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	p := identity.Principal{
		Subject: "ada", DisplayName: "ada", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}

	switch token {
	case adminToken:
		return p, nil
	case storedToken:
		p.TokenID = "tok-ada"

		return p, nil
	case personToken:
		return identity.Principal{
			Subject: "tm", DisplayName: "tm", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
			Credential: identity.CredentialSession, JobID: "",
		}, nil
	case directToken:
		return identity.Principal{
			Subject: "direct", DisplayName: "direct", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
			Credential: identity.CredentialSession, JobID: "",
		}, nil
	default:
		return identity.Principal{}, identity.ErrUnauthenticated
	}
}

// holdsOnly allows the actions it lists on every object, and denies the rest.
type holdsOnly struct {
	actions []identity.Action
}

func (h holdsOnly) Authorize(_ context.Context, _ identity.Principal, action identity.Action, _ identity.Object) error {
	if slices.Contains(h.actions, action) {
		return nil
	}

	return identity.ErrDenied
}

var errNoListing = errors.New("no route lists objects")

func (holdsOnly) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// allActions holds every action that the routes and the service check.
func allActions() holdsOnly {
	return holdsOnly{actions: []identity.Action{
		identity.ActionNamespaceRead, identity.ActionNamespaceReadAccess, identity.ActionNamespaceManageTrustRules,
		identity.ActionPrincipalManageTokens,
	}}
}

// env is a store with the handler over it, on a test server, with the
// namespaces acme and other, and a service principal in each.
type env struct {
	t       *testing.T
	store   *sqlite.Store
	audit   *testAudit
	server  *httptest.Server
	handler *trustrules.Handler
	tokens  *accesstoken.Service
	// worker is a service principal of acme, and stranger one of other.
	worker, stranger string
}

// newEnv opens a store, creates the namespaces and the principals, and
// serves the handler with the authorizer authz.
func newEnv(t *testing.T, authz identity.Authorizer) *env {
	t.Helper()

	return newEnvWithReach(t, authz, nil)
}

// newEnvWithReach is newEnv with a wrapper around the reach port of the
// trust rule service. A nil wrap keeps the token service.
func newEnvWithReach(
	t *testing.T, authz identity.Authorizer, wrap func(trustrulesvc.Reach) trustrulesvc.Reach,
) *env {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	e := &env{
		t: t, store: s, audit: newTestAudit(), server: nil, handler: nil, tokens: nil, worker: "", stranger: "",
	}

	for _, name := range []namespace.Name{"acme", "other"} {
		ns := namespace.Namespace{
			ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: name,
			PrimaryInstallation: installationName, FailoverVersion: 1,
			Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		}
		if err := s.CreateNamespace(t.Context(), ns); err != nil {
			t.Fatalf("create namespace: %v", err)
		}
	}

	e.worker = e.principal("acme", principal.KindService)
	e.stranger = e.principal("other", principal.KindService)
	e.serve(authz, wrap)

	return e
}

// serve starts the handler on a test server, with the reach port of the
// trust rule service wrapped by wrap when it is not nil.
func (e *env) serve(authz identity.Authorizer, wrap func(trustrulesvc.Reach) trustrulesvc.Reach) {
	e.t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: e.store, Now: nil, Installation: installationName})
	if err != nil {
		e.t.Fatalf("namespace service: %v", err)
	}

	// The token service reads the reach from the store, so the trust rule
	// service compares reaches as the server does.
	tokens, err := accesstoken.NewService(accesstoken.Config{
		Makers: nil,
		Reach: access.ReachConfig{
			Grants: e.store, Roles: e.store, Callers: e.store, Principals: e.store,
			Collections: nil, Units: e.store,
		},
		Store: e.store, Principals: e.store, Freshness: nil, Authorizer: nil, TempAdmins: nil, Logger: nil,
		Now: testNow, NewID: nil,
		Guard: freshstep.Guard{},
	})
	if err != nil {
		e.t.Fatalf("token service: %v", err)
	}

	e.tokens = tokens

	var reach trustrulesvc.Reach = tokens
	if wrap != nil {
		reach = wrap(reach)
	}

	rules, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: e.store.TrustRules(), Namespaces: namespaces, Principals: e.store, Authorizer: authz,
		Reach:            reach,
		InstallationURLs: []string{publicURL}, OwnIssuer: nil, CheckURL: nil, Now: testNow,
		Bundles: e.store.TrustRules(), Sessions: nil,
	})
	if err != nil {
		e.t.Fatalf("trust rule service: %v", err)
	}

	exchanger, err := trustrulesvc.NewExchanger(trustrulesvc.ExchangeConfig{
		Rules: e.store.TrustRules(), Exchanges: e.store.TrustRules(), Namespaces: namespaces, Principals: e.store,
		Tokens: tokens, Audit: e.store, ServerAudit: e.store, Refusals: nil, Keys: nil,
		InstallationURLs: []string{publicURL}, OwnIssuer: nil, Now: testNow, Log: nil,
	})
	if err != nil {
		e.t.Fatalf("exchanger: %v", err)
	}

	h, err := trustrules.NewHandler(trustrules.Config{
		Exchanger: exchanger, MtlsExchanger: nil, MtlsGuard: nil,
		Namespaces: namespaces, Rules: rules, Authenticator: tokenAuthenticator{}, Authorizer: authz,
		Audit: e.audit, OnAuditFailure: nil, OnMtlsRefused: nil, DenialLimiter: nil, Sessions: nil,
		Logger: slog.New(slog.DiscardHandler), Now: nil,
	})
	if err != nil {
		e.t.Fatalf("handler: %v", err)
	}

	srv := httptest.NewServer(withTestSession(h))
	e.t.Cleanup(srv.Close)

	e.server, e.handler = srv, h
}

// principal makes a principal of the given kind in ns, or of the
// installation when ns is empty, and returns its ID.
func (e *env) principal(ns namespace.Name, kind principal.Kind) string {
	e.t.Helper()

	id := ident.NewID(principal.IDPrefix)

	return e.storePrincipal(id, "subject-"+id, ns, kind)
}

// principalWithSubject makes a principal of the given kind in ns, or of the
// installation when ns is empty, with the subject subject, and returns its
// ID.
func (e *env) principalWithSubject(subject string, ns namespace.Name, kind principal.Kind) string {
	e.t.Helper()

	return e.storePrincipal(ident.NewID(principal.IDPrefix), subject, ns, kind)
}

// storePrincipal stores a principal with the ID id and the subject given,
// of the given kind, in ns, or of the installation when ns is empty, and
// returns id.
func (e *env) storePrincipal(id, subject string, ns namespace.Name, kind principal.Kind) string {
	e.t.Helper()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: subject,
		DisplayName: subject, Kind: kind, Source: principal.SourceLocal, Namespace: ns, GroupCount: 0,
		BreakGlass: false,
	}

	scope := ns
	if scope == "" {
		scope = audit.ServerScope
	}

	entry := audit.Entry{
		At: at, Namespace: scope, Subject: "admin", IDPSubject: "", TokenID: "", Action: "server:manage-principals",
		Object: principal.Ref(id), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "",
		JobID: "", Reason: "",
		Origin: nil,
	}

	var err error
	if ns == "" {
		err = e.store.CreatePrincipal(e.t.Context(), p, entry)
	} else {
		n, getErr := e.store.GetNamespace(e.t.Context(), ns)
		if getErr != nil {
			e.t.Fatalf("read namespace: %v", getErr)
		}

		err = e.store.CreateNamespacePrincipal(e.t.Context(), p, n.Fence(), entry)
	}

	if err != nil {
		e.t.Fatalf("create principal: %v", err)
	}

	return id
}

// sessionHeader makes the test server carry a console session with a sign-in
// of now on the request, as the session checker of the server does.
const sessionHeader = "X-Test-Session"

// testNow is the fixed clock of the service and of the sign-in of a test
// session, so that no test reads the wall clock.
func testNow() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

// withTestSession wraps h so that a request with sessionHeader carries a
// console session in its context. The value "1" signs in at the fixed now of
// the service, and the value "old" signs in 16 minutes before it: one minute
// past the default freshness window of 15 minutes, so a write that needs a
// fresh authentication is refused as stale and not as missing.
func withTestSession(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := testNow()

		if r.Header.Get(sessionHeader) == "old" {
			at = testNow().Add(-16 * time.Minute)
		}

		if v := r.Header.Get(sessionHeader); v == "1" || v == "old" {
			ctx := sessionauth.WithSession(r.Context())
			ctx = sessionauth.WithSignIn(ctx, sessionauth.SignIn{At: at, Provider: "local"})
			r = r.WithContext(ctx)
		}

		h.ServeHTTP(w, r)
	})
}

// do sends a request with the given token and body, with a fresh console
// session.
func (e *env) do(method, path, token, body string) (int, []byte) {
	e.t.Helper()

	return e.send(method, path, token, body, map[string]string{sessionHeader: "1"})
}

// send sends a request. The header map holds more request headers.
func (e *env) send(method, path, token, body string, header map[string]string) (int, []byte) {
	e.t.Helper()

	req, err := http.NewRequestWithContext(e.t.Context(), method, e.server.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}

	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	for k, v := range header {
		req.Header.Set(k, v)
	}

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("send %s %s: %v", method, path, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			e.t.Errorf("close body: %v", closeErr)
		}
	}()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, out
}

// admin sends a request as the administrator.
func (e *env) admin(method, path, body string) (int, []byte) {
	e.t.Helper()

	return e.do(method, path, adminToken, body)
}

// ruleBody returns the body of a create of a rule called name for the
// principal principalID.
func ruleBody(name, principalID string) string {
	return `{"name":` + quote(name) + `,"issuer":"https://token.actions.example.com",` +
		`"audience":"https://dustpan.example.com/acme","keySource":"discovery",` +
		`"principal":` + quote(principalID) + `,` +
		`"conditions":[{"claim":"sub","values":["repo:acme/infra:ref:refs/heads/main"]},` +
		`{"claim":"repository","values":["acme/infra"]}]}`
}

// quote returns s as a JSON string.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}

	return string(b)
}

// condition is the wire form of a condition, as a test reads it.
type condition struct {
	Claim  string   `json:"claim"`
	Values []string `json:"values"`
}

// rule is the wire form of a rule, as a test reads it.
type rule struct {
	ID         string      `json:"id"`
	Namespace  string      `json:"namespace"`
	Name       string      `json:"name"`
	Issuer     string      `json:"issuer"`
	Audience   string      `json:"audience"`
	KeySource  string      `json:"keySource"`
	StaticKeys string      `json:"staticKeys"`
	Principal  string      `json:"principal"`
	CreatedBy  string      `json:"createdBy"`
	CreatedAt  string      `json:"createdAt"`
	Conditions []condition `json:"conditions"`
}

func decode(t *testing.T, body []byte, v any) {
	t.Helper()

	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// errorCode returns the code of an error answer.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()

	var got struct {
		ErrorCode string `json:"errorCode"`
	}

	decode(t, body, &got)

	return got.ErrorCode
}

// ruleEntries returns the audit entries of trust rule changes of acme that
// the store keeps, oldest first.
func (e *env) ruleEntries() []audit.Entry {
	e.t.Helper()

	listing, err := e.store.List(e.t.Context(), "acme", audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		e.t.Fatalf("list audit: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if strings.HasPrefix(listing.Entries[i].Action, "trust-rule:") {
			out = append(out, listing.Entries[i])
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out
}

// mustCreate creates the rule name for the principal worker, as the
// administrator, and returns it.
func (e *env) mustCreate(name string) rule {
	e.t.Helper()

	status, body := e.admin(http.MethodPost, rulesURL, ruleBody(name, e.worker))
	if status != http.StatusCreated {
		e.t.Fatalf("create %q = %d %s, want 201", name, status, body)
	}

	var out rule
	decode(e.t, body, &out)

	return out
}

// mustGet reads the rule id of acme.
func (e *env) mustGet(id string) rule {
	e.t.Helper()

	status, body := e.admin(http.MethodGet, rulesURL+"/"+id, "")
	if status != http.StatusOK {
		e.t.Fatalf("get %s = %d %s", id, status, body)
	}

	var out rule
	decode(e.t, body, &out)

	return out
}

// mustList reads the rules of acme.
func (e *env) mustList() []rule {
	e.t.Helper()

	status, body := e.admin(http.MethodGet, rulesURL, "")
	if status != http.StatusOK {
		e.t.Fatalf("list = %d %s", status, body)
	}

	var out struct {
		Rules []rule `json:"rules"`
	}
	decode(e.t, body, &out)

	return out.Rules
}
