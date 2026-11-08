package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	devCodeRoute      = "/api/dustpan/v1/sign-in/dev-code"
	capabilitiesRoute = "/api/dustpan/v1/capabilities"
)

// devEnv is a handler over a real store with the dev sign-in, the real access
// authorizer and the clock that the code and the sessions share.
type devEnv struct {
	handler testHandler
	store   *sqlite.Store
	access  *access.Service
	limiter *fakeLimiter
	clock   *envClock
	// code is the text of the one-time code, empty when the handler has none.
	code string
	// rebuild makes a handler over the same store, with the parts changed.
	rebuild func(devParts) testHandler
}

// devEnvOptions are the options of newDevEnv. noCode gives the handler no
// one-time code. authz replaces the access authorizer when it is not nil.
type devEnvOptions struct {
	authz  identity.Authorizer
	kind   installation.Kind
	noCode bool
	// noAccess gives the handler no access service. noAdmin gives the dev
	// sign-in no bootstrap administrator.
	noAccess bool
	noAdmin  bool
	// grantFails makes each write of a grant fail.
	grantFails bool
}

// bootstrapAdmin is the bootstrap administrator that the dev sign-in grants
// as: the principal of the token of dev.
var bootstrapAdmin = identity.Principal{ //nolint:gochecknoglobals // A frozen test value.
	TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "admin",
	Admin: true, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
}

// noBootstrapAdmin is the zero principal of a sign-in with no administrator.
var noBootstrapAdmin = identity.Principal{ //nolint:gochecknoglobals // A frozen test value.
	TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "", DisplayName: "",
	Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
}

// devParts are the parts of a handler that rebuild changes: a test builds a
// second handler over the same store, as a restart with other settings does.
type devParts struct {
	kind installation.Kind
	// code replaces the one-time code of the handler. Nil keeps the code of
	// the environment.
	code *devcode.Code
	// noAccess, noAdmin and grantFails: see devEnvOptions. grantFails makes
	// the store of the access service fail each write of a grant.
	noAccess   bool
	noAdmin    bool
	grantFails bool
	noCode     bool
	// stateWrite is the effective value of the state.write gates that the
	// capabilities document shows.
	stateWrite stategate.Effective
}

// errGrantStore is the error of failingGrants.
var errGrantStore = errors.New("grant store failed")

// failingGrants is an access store that fails each write of a grant.
type failingGrants struct {
	access.Store
}

func (failingGrants) CreateGrant(context.Context, access.Grant, namespace.Fence, audit.Entry, access.GrantCheck) error {
	return errGrantStore
}

// devDeps are the services that every handler of a devEnv shares.
type devDeps struct {
	st         *sqlite.Store
	namespaces *namespace.Service
	policies   *session.Policies
	sessions   *session.Sessions
	grants     *access.Service
	principals *principal.Service
	tokens     *accesstoken.Service
	authz      identity.Authorizer
	limiter    *fakeLimiter
	code       *devcode.Code
}

// accessFor returns the access service that parts asks for.
func (d devDeps) accessFor(t *testing.T, parts devParts) *access.Service {
	t.Helper()

	switch {
	case parts.noAccess:
		return nil
	case parts.grantFails:
		svc, err := access.NewService(access.ServiceConfig{
			Store: failingGrants{Store: d.st}, Directory: d.st, Namespaces: d.st, Authorizer: d.grants.Authorizer(),
			Reach: access.ReachConfig{
				Grants: d.st, Roles: d.st, Callers: d.st, Principals: d.st, Collections: nil, Units: d.st,
			},
			Now: nil, NewID: nil, BootstrapAdmins: nil, LimitGroups: nil,
		})
		if err != nil {
			t.Fatalf("failing access service: %v", err)
		}

		return svc
	default:
		return d.grants
	}
}

// handler builds a handler over the services with the parts.
func (d devDeps) handler(t *testing.T, parts devParts) testHandler {
	t.Helper()

	devAdmin := bootstrapAdmin
	if parts.noAdmin {
		devAdmin = noBootstrapAdmin
	}

	handlerCode := d.code
	if parts.code != nil {
		handlerCode = parts.code
	}

	if parts.noCode {
		handlerCode = nil
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       d.authz,
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       d.namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           d.st,
		Journal:          d.st,
		Principals:       d.principals,
		Access:           d.accessFor(t, parts),
		Tokens:           d.tokens,
		Audit:            d.st,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    d.policies,
		CheckpointEpochs: nil,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     stubSweeper{},
		TempAdmins:       nil,
		Now:              nil,
		AuditSinks:       nil,
		SignIn: &apiserver.SignIn{
			Service:             newTestSignInService(t, d.st, d.sessions, d.principals),
			OnDevRefused:        nil,
			Sessions:            d.sessions,
			Limiter:             d.limiter,
			Stored:              nil,
			Admin:               nil,
			PasswordProviders:   nil,
			Origins:             []string{signInBase},
			PasswordLimiter:     nil,
			Passkeys:            nil,
			Providers:           nil,
			DevCode:             handlerCode,
			DevAdmin:            devAdmin,
			PasskeySignIn:       nil,
			PasskeyBeginLimiter: nil,
			PasskeyLimiter:      nil,
		},
		InstallationKind: parts.kind,
		StateWriteGates:  parts.stateWrite,
		Workers:          nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// newDevDeps builds the services of a devEnv over a new store.
func newDevDeps(t *testing.T, clock *envClock, code *devcode.Code, authz identity.Authorizer) devDeps {
	t.Helper()

	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, seedErr := namespaces.Create(t.Context(), testNamespace); seedErr != nil {
		t.Fatalf("seed namespace: %v", seedErr)
	}

	policies, err := session.NewPolicies(session.PolicyConfig{Store: st, Now: clock.Now})
	if err != nil {
		t.Fatalf("session policies: %v", err)
	}

	sessions, err := session.NewSessions(
		session.SessionsConfig{Store: st, Providers: st, Inactivity: nil, Now: clock.Now},
	)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	grants := seedRouteAccess(t, st)
	if authz == nil {
		authz = grants.Authorizer()
	}

	return devDeps{
		st: st, namespaces: namespaces, policies: policies, sessions: sessions, grants: grants,
		principals: seedRoutePrincipals(t, st), tokens: seedRouteTokens(t, st), authz: authz,
		limiter: &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}, code: code,
	}
}

func newDevEnv(t *testing.T, opts devEnvOptions) devEnv {
	t.Helper()

	clock := &envClock{mu: sync.Mutex{}, now: time.Now().UTC().Truncate(time.Second)}

	var (
		code *devcode.Code
		text string
		err  error
	)

	if !opts.noCode {
		if code, text, err = devcode.Make(clock.Now); err != nil {
			t.Fatalf("make code: %v", err)
		}
	}

	deps := newDevDeps(t, clock, code, opts.authz)

	handler := deps.handler(t, devParts{
		kind: opts.kind, code: nil, noAccess: opts.noAccess, noAdmin: opts.noAdmin, grantFails: opts.grantFails,
		noCode: opts.noCode, stateWrite: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
	})

	return devEnv{
		handler: handler, store: deps.st, access: deps.grants, limiter: deps.limiter, clock: clock, code: text,
		rebuild: func(parts devParts) testHandler { return deps.handler(t, parts) },
	}
}

// post sends a POST request with body to the dev sign-in.
func (e devEnv) post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, devCodeRoute, strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}

// get sends a GET request with the cookies and the bearer token to target.
func (e devEnv) get(t *testing.T, target, token string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	for _, c := range cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
}

func codeBody(code string) string {
	raw, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		panic(err)
	}

	return string(raw)
}

// TestDevCodeRejected proves that a request that does not carry the code
// answers 403 with one text, spends one unit of the failure budget and makes
// no session.
func TestDevCodeRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body func(code string) string
	}{
		{name: "wrong code", body: func(string) string { return codeBody("not-the-code") }},
		{name: "empty code", body: func(string) string { return codeBody("") }},
		{name: "no body", body: func(string) string { return "" }},
		{name: "body that is not JSON", body: func(string) string { return "code=abc" }},
		{name: "unknown field", body: func(code string) string { return `{"code":"` + code + `","extra":1}` }},
		{name: "code in an array", body: func(code string) string { return `["` + code + `"]` }},
		{name: "body over the cap", body: func(code string) string {
			return `{"code":"` + code + strings.Repeat("a", 4096) + `"}`
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newDevEnv(
				t,
				devEnvOptions{
					authz:      nil,
					kind:       installation.KindDev,
					noCode:     false,
					noAccess:   false,
					noAdmin:    false,
					grantFails: false,
				},
			)

			checkDevCodeRejected(t, env, env.post(t, tt.body(env.code)))
		})
	}
}

// checkDevCodeRejected proves the answer of a request that does not carry the
// code: 403 with no session cookie and no code in the text, one unit of the
// failure budget spent, and the code still good.
func checkDevCodeRejected(t *testing.T, env devEnv, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}

	if got := cookieOf(rec, apiserver.SessionCookie); got != nil {
		t.Fatalf("a rejected request set the session cookie %q", got.Name)
	}

	if got := env.limiter.count(); got != 1 {
		t.Fatalf("failure budget spent %d times, want 1", got)
	}

	if strings.Contains(rec.Body.String(), env.code) {
		t.Fatal("the answer holds the code")
	}

	// A wrong request never spends the code.
	if again := env.post(t, codeBody(env.code)); again.Code != http.StatusNoContent {
		t.Fatalf("right code after a wrong request: status = %d, want 204", again.Code)
	}
}

// TestDevCodeSignsInOnce proves that the code signs a browser in as the dev
// administrator one time: the first request answers 204 with a session that
// holds the site-administrator role, and the second answers 403.
func TestDevCodeSignsInOnce(t *testing.T) {
	t.Parallel()

	env := newDevEnv(
		t,
		devEnvOptions{
			authz:      nil,
			kind:       installation.KindDev,
			noCode:     false,
			noAccess:   false,
			noAdmin:    false,
			grantFails: false,
		},
	)

	first := env.post(t, codeBody(env.code))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first request: status = %d, want 204: %s", first.Code, first.Body)
	}

	cookie := cookieOf(first, apiserver.SessionCookie)
	if cookie == nil {
		t.Fatal("first request set no session cookie")
	}

	checkBrowserCookie(t, apiserver.SessionCookie, cookie, http.SameSiteStrictMode)

	if got := env.limiter.count(); got != 0 {
		t.Fatalf("failure budget spent %d times by a right code, want 0", got)
	}

	read := env.get(t, "/api/server/session", "", cookie)
	if read.Code != http.StatusOK {
		t.Fatalf("read session: status = %d, want 200: %s", read.Code, read.Body)
	}

	var got struct {
		Subject  string `json:"subject"`
		Provider string `json:"provider"`
	}

	if err := json.Unmarshal(read.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	if got.Subject != "dev:admin" || got.Provider != "dev" {
		t.Fatalf("session = %+v, want subject dev:admin and provider dev", got)
	}

	// The session is an administrator: it reads the principals of the
	// installation, which needs a site-administrator grant.
	if list := env.get(t, "/api/server/principals", "", cookie); list.Code != http.StatusOK {
		t.Fatalf("list principals with the session: status = %d, want 200: %s", list.Code, list.Body)
	}

	second := env.post(t, codeBody(env.code))
	if second.Code != http.StatusForbidden {
		t.Fatalf("second request: status = %d, want 403: %s", second.Code, second.Body)
	}

	if cookieOf(second, apiserver.SessionCookie) != nil {
		t.Fatal("second request set a session cookie")
	}

	if got := env.limiter.count(); got != 1 {
		t.Fatalf("failure budget spent %d times after the second request, want 1", got)
	}
}

// TestDevCodeExpires proves that the code stops working when its lifetime
// ends.
func TestDevCodeExpires(t *testing.T) {
	t.Parallel()

	env := newDevEnv(
		t,
		devEnvOptions{
			authz:      nil,
			kind:       installation.KindDev,
			noCode:     false,
			noAccess:   false,
			noAdmin:    false,
			grantFails: false,
		},
	)

	env.clock.Add(devcode.Lifetime)

	rec := env.post(t, codeBody(env.code))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
}

// TestDevCodeRefusedByLimiter proves that the route obeys the failure limit
// of the address, as each sign-in route does.
func TestDevCodeRefusedByLimiter(t *testing.T) {
	t.Parallel()

	env := newDevEnv(
		t,
		devEnvOptions{
			authz:      nil,
			kind:       installation.KindDev,
			noCode:     false,
			noAccess:   false,
			noAdmin:    false,
			grantFails: false,
		},
	)
	env.limiter.refuse = true

	rec := env.post(t, codeBody(env.code))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", rec.Code, rec.Body)
	}
}

// TestDevCodeRouteNeedsCode proves that a handler with no one-time code
// serves no dev route, whatever its kind.
func TestDevCodeRouteNeedsCode(t *testing.T) {
	t.Parallel()

	for _, kind := range []installation.Kind{"", installation.KindDev, installation.KindProduction} {
		t.Run("kind "+kind.String(), func(t *testing.T) {
			t.Parallel()

			env := newDevEnv(
				t,
				devEnvOptions{authz: nil, kind: kind, noCode: true, noAccess: false, noAdmin: false, grantFails: false},
			)

			rec := env.post(t, codeBody("anything"))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}

			for _, route := range env.handler.Routes() {
				if route.Path == devCodeRoute {
					t.Fatalf("route table holds %s with no code", route.Path)
				}
			}
		})
	}
}

// TestCapabilities proves the capabilities document: every caller gets the
// API version, and a signed-in caller with caller:read gets the installation
// kind too.
func TestCapabilities(t *testing.T) {
	t.Parallel()

	denyCaller := selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionCallerRead: true}}

	tests := []struct {
		name     string
		kind     installation.Kind
		authz    identity.Authorizer
		token    string
		wantKind string
	}{
		{name: "no sign-in", kind: installation.KindDev, authz: nil, token: "", wantKind: ""},
		{
			name: "wrong token", kind: installation.KindDev, authz: selectiveAuthorizer{deny: nil}, token: "wrong",
			wantKind: "",
		},
		{
			name: "signed in, dev", kind: installation.KindDev, authz: selectiveAuthorizer{deny: nil},
			token: "test-token", wantKind: "dev",
		},
		{
			name: "signed in, production", kind: installation.KindProduction, authz: selectiveAuthorizer{deny: nil},
			token: "test-token", wantKind: "production",
		},
		{
			name: "signed in, no kind set means production", kind: "", authz: selectiveAuthorizer{deny: nil},
			token: "test-token", wantKind: "production",
		},
		{
			name:     "signed in without caller:read",
			kind:     installation.KindDev,
			authz:    denyCaller,
			token:    "test-token",
			wantKind: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newDevEnv(
				t,
				devEnvOptions{
					authz:      tt.authz,
					kind:       tt.kind,
					noCode:     true,
					noAccess:   false,
					noAdmin:    false,
					grantFails: false,
				},
			)

			checkCapabilities(t, env.get(t, capabilitiesRoute, tt.token), tt.wantKind)
		})
	}
}

// checkCapabilities proves the capabilities document in rec: status 200, the
// API version, and the installation kind wantKind, or no kind field when
// wantKind is empty.
func checkCapabilities(t *testing.T, rec *httptest.ResponseRecorder, wantKind string) {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode document: %v", err)
	}

	if doc["apiVersion"] != "v1" {
		t.Fatalf("apiVersion = %v, want v1", doc["apiVersion"])
	}

	got, present := doc["installationKind"]

	switch {
	case wantKind == "" && present:
		t.Fatalf("installationKind = %v, want the field absent", got)
	case wantKind != "" && got != wantKind:
		t.Fatalf("installationKind = %v, want %q", got, wantKind)
	default:
	}
}

// TestCapabilitiesShowStateWriteGates proves that the capabilities document
// shows the effective value of the state.write gates and its reason to a
// caller with caller:read, and hides it from any other caller.
func TestCapabilitiesShowStateWriteGates(t *testing.T) {
	t.Parallel()

	off := stategate.Effective{
		Mode: stategate.ModeOff, Reason: "the namespace store keeps no gates", Checked: []stategate.WritePath{},
		Unchecked: []stategate.WritePath{stategate.PathTFELock, stategate.PathPulumiUpdate},
	}
	on := stategate.Effective{
		Mode:   stategate.ModeOn,
		Reason: stategate.ReasonOn,
		Checked: []stategate.WritePath{
			stategate.PathTFELock,
		},
		Unchecked: []stategate.WritePath{stategate.PathPulumiUpdate},
	}
	allow := selectiveAuthorizer{deny: nil}
	denyCaller := selectiveAuthorizer{deny: map[identity.Action]bool{identity.ActionCallerRead: true}}

	tests := []struct {
		name  string
		value stategate.Effective
		authz identity.Authorizer
		token string
		want  string
	}{
		{
			"off", off, allow, "test-token",
			`"stateWriteGates":{"mode":"off","reason":"the namespace store keeps no gates","checked":[],` +
				`"unchecked":["opentofu.tfe.lock","pulumi.update"]}`,
		},
		{
			"on", on, allow, "test-token",
			`"stateWriteGates":{"mode":"on","reason":"` + stategate.ReasonOn + `","checked":["opentofu.tfe.lock"],` +
				`"unchecked":["pulumi.update"]}`,
		},
		{
			"nil lists are empty lists",
			stategate.Effective{Mode: stategate.ModeOn, Reason: "r", Checked: nil, Unchecked: nil},
			allow, "test-token",
			`"stateWriteGates":{"mode":"on","reason":"r","checked":[],"unchecked":[]}`,
		},
		{"no sign-in", off, allow, "", ""},
		{"signed in without caller:read", off, denyCaller, "test-token", ""},
		{"no value", stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil}, allow, "test-token", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newDevEnv(t, devEnvOptions{
				authz: tt.authz, kind: installation.KindProduction, noCode: true,
				noAccess: false, noAdmin: false, grantFails: false,
			})
			handler := env.rebuild(devParts{
				kind: installation.KindProduction, code: nil, noAccess: false, noAdmin: false, grantFails: false,
				noCode: true, stateWrite: tt.value,
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, capabilitiesRoute, nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			body := rec.Body.String()

			switch {
			case rec.Code != http.StatusOK:
				t.Fatalf("status = %d, want 200: %s", rec.Code, body)
			case tt.want == "" && strings.Contains(body, "stateWriteGates"):
				t.Fatalf("body = %s, want no stateWriteGates", body)
			case tt.want != "" && !strings.Contains(body, tt.want):
				t.Fatalf("body = %s, want %s", body, tt.want)
			default:
			}
		})
	}
}

// TestCapabilitiesWithDevSession proves that the browser session of the dev
// sign-in reads the installation kind.
func TestCapabilitiesWithDevSession(t *testing.T) {
	t.Parallel()

	env := newDevEnv(
		t,
		devEnvOptions{
			authz:      nil,
			kind:       installation.KindDev,
			noCode:     false,
			noAccess:   false,
			noAdmin:    false,
			grantFails: false,
		},
	)

	signedIn := env.post(t, codeBody(env.code))
	cookie := cookieOf(signedIn, apiserver.SessionCookie)

	if cookie == nil {
		t.Fatalf("sign-in set no session cookie: status %d", signedIn.Code)
	}

	rec := env.get(t, capabilitiesRoute, "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"installationKind":"dev"`) {
		t.Fatalf("capabilities with the session: status = %d, body = %s", rec.Code, rec.Body)
	}
}

// devWrites reports what the dev sign-in has written to the store: whether
// the person dev:admin exists, and the count of the grants on the server
// object.
func (e devEnv) devWrites(t *testing.T) (bool, int64) {
	t.Helper()

	_, err := e.store.GetPrincipalBySubject(t.Context(), principal.DevAdminSubject)
	if err != nil && !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("read the dev person: %v", err)
	}

	page, listErr := e.access.ListGrants(
		t.Context(),
		identity.ServerObject(),
		access.PageRequest{Cursor: "", Holder: "", Role: "", Limit: 100},
	)
	if listErr != nil {
		t.Fatalf("list grants: %v", listErr)
	}

	return err == nil, page.Total
}

// postTo sends a POST request with body to the dev sign-in of handler.
func postTo(t *testing.T, handler testHandler, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, devCodeRoute, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

// TestDevCodeRouteNeedsDevKind proves the layer of the route table: a handler
// that holds a code and has the kind production, or no kind, mounts no dev
// route, so a request gets 404 and spends nothing.
func TestDevCodeRouteNeedsDevKind(t *testing.T) {
	t.Parallel()

	for _, kind := range []installation.Kind{"", installation.KindProduction, "unknown"} {
		t.Run("kind "+kind.String(), func(t *testing.T) {
			t.Parallel()

			env := newDevEnv(t, devEnvOptions{
				authz: nil, kind: kind, noCode: false, noAccess: false, noAdmin: false, grantFails: false,
			})

			for _, route := range env.handler.Routes() {
				if route.Path == devCodeRoute {
					t.Fatalf("route table holds %s for the kind %q", route.Path, kind)
				}
			}

			if rec := env.post(t, codeBody(env.code)); rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}

			if exists, _ := env.devWrites(t); exists {
				t.Fatal("the dev person exists after a request to an unmounted route")
			}
		})
	}
}

// TestDevCodeHandlerRefusesOtherKinds proves the layer of the handler: with
// the route table out of the way, the handler function alone refuses a
// right code on a handler of the kind production or no kind. The code stays
// unspent, nothing is written, and the same code signs in on a dev handler.
func TestDevCodeHandlerRefusesOtherKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range []installation.Kind{"", installation.KindProduction} {
		t.Run("kind "+kind.String(), func(t *testing.T) {
			t.Parallel()

			env := newDevEnv(t, devEnvOptions{
				authz: nil, kind: kind, noCode: false, noAccess: false, noAdmin: false, grantFails: false,
			})

			_, grantsBefore := env.devWrites(t)

			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, devCodeRoute, strings.NewReader(codeBody(env.code)),
			)
			rec := httptest.NewRecorder()
			apiserver.DevCodeSignInForTest(env.handler.Handler, rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
			}

			if cookieOf(rec, apiserver.SessionCookie) != nil {
				t.Fatal("the refused request set a session cookie")
			}

			if exists, grants := env.devWrites(t); exists || grants != grantsBefore {
				t.Fatalf("person exists = %v, grants = %d (was %d), want nothing written", exists, grants, grantsBefore)
			}

			dev := env.rebuild(devParts{
				kind:       installation.KindDev,
				code:       nil,
				noAccess:   false,
				noAdmin:    false,
				grantFails: false,
				noCode:     false, stateWrite: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
			})

			if again := postTo(t, dev, codeBody(env.code)); again.Code != http.StatusNoContent {
				t.Fatalf("code on a dev handler after the refusal: status = %d, want 204: %s", again.Code, again.Body)
			}
		})
	}
}

// TestDevCodeFailsClosedAndStaysUnspent proves that a handler with no access
// service, or no bootstrap administrator, refuses before it spends the code
// and before it writes the person or a grant, and that a valid post works
// once a handler with both gets the same code.
func TestDevCodeFailsClosedAndStaysUnspent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts devParts
	}{
		{name: "no access service", parts: devParts{
			kind:       installation.KindDev,
			code:       nil,
			noAccess:   true,
			noAdmin:    false,
			grantFails: false,
			noCode:     false,
			stateWrite: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		}},
		{name: "no bootstrap administrator", parts: devParts{
			kind:       installation.KindDev,
			code:       nil,
			noAccess:   false,
			noAdmin:    true,
			grantFails: false,
			noCode:     false,
			stateWrite: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newDevEnv(t, devEnvOptions{
				authz: nil, kind: installation.KindDev, noCode: false, noAccess: false, noAdmin: false,
				grantFails: false,
			})

			checkFailsClosed(t, env, tt.parts)

			// The code is unspent: the same code signs in once the handler has both.
			ok := postTo(t, env.handler, codeBody(env.code))
			if ok.Code != http.StatusNoContent || cookieOf(ok, apiserver.SessionCookie) == nil {
				t.Fatalf("valid post after the failure: status = %d, want 204 with a cookie: %s", ok.Code, ok.Body)
			}
		})
	}
}

// checkFailsClosed proves that a post with a right code to the handler of
// parts answers a server error, sets no cookie, writes no person and no
// grant, and spends nothing of the failure budget.
func checkFailsClosed(t *testing.T, env devEnv, parts devParts) {
	t.Helper()

	_, grantsBefore := env.devWrites(t)

	rec := postTo(t, env.rebuild(parts), codeBody(env.code))
	if rec.Code < http.StatusInternalServerError {
		t.Fatalf("status = %d, want a server error: %s", rec.Code, rec.Body)
	}

	if cookieOf(rec, apiserver.SessionCookie) != nil {
		t.Fatal("the failed sign-in set a session cookie")
	}

	if exists, grants := env.devWrites(t); exists || grants != grantsBefore {
		t.Fatalf("person exists = %v, grants = %d (was %d), want nothing written", exists, grants, grantsBefore)
	}

	if got := env.limiter.count(); got != 0 {
		t.Fatalf("failure budget spent %d times by a failure of the setup, want 0", got)
	}
}

// TestDevCodeGrantFails proves what a failed grant leaves: the answer is an
// error with no session cookie and no grant. The code is spent, because the
// check of the code comes first. The person dev:admin stays, with no grant
// and no session, so it can do nothing, and the next sign-in with a new code
// finds the same person, gives it the role and starts a session.
func TestDevCodeGrantFails(t *testing.T) {
	t.Parallel()

	env := newDevEnv(t, devEnvOptions{
		authz: nil, kind: installation.KindDev, noCode: false, noAccess: false, noAdmin: false, grantFails: true,
	})

	_, grantsBefore := env.devWrites(t)

	rec := env.post(t, codeBody(env.code))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body)
	}

	if cookieOf(rec, apiserver.SessionCookie) != nil {
		t.Fatal("the failed grant set a session cookie")
	}

	exists, grants := env.devWrites(t)
	if !exists || grants != grantsBefore {
		t.Fatalf(
			"person exists = %v, grants = %d (was %d), want the person and no new grant",
			exists,
			grants,
			grantsBefore,
		)
	}

	first, err := env.store.GetPrincipalBySubject(t.Context(), principal.DevAdminSubject)
	if err != nil {
		t.Fatalf("read the dev person: %v", err)
	}

	if again := env.post(t, codeBody(env.code)); again.Code != http.StatusForbidden {
		t.Fatalf("spent code: status = %d, want 403", again.Code)
	}

	code, text, err := devcode.Make(env.clock.Now)
	if err != nil {
		t.Fatalf("make code: %v", err)
	}

	ok := postTo(t, env.rebuild(devParts{
		kind:       installation.KindDev,
		code:       code,
		noAccess:   false,
		noAdmin:    false,
		grantFails: false,
		noCode:     false,
		stateWrite: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
	}), codeBody(text))
	if ok.Code != http.StatusNoContent {
		t.Fatalf("sign-in with a new code: status = %d, want 204: %s", ok.Code, ok.Body)
	}

	second, err := env.store.GetPrincipalBySubject(t.Context(), principal.DevAdminSubject)
	if err != nil || second.ID != first.ID {
		t.Fatalf("person after the retry = %+v, %v; want the person of the failed grant (%s)", second, err, first.ID)
	}

	if _, after := env.devWrites(t); after != grantsBefore+1 {
		t.Fatalf("grants after the retry = %d, want %d", after, grantsBefore+1)
	}
}

// TestDevCodeEarlyFailureWritesNothing proves that a sign-in that fails
// before the person is made writes no person and no grant: a wrong code, an
// empty body and a refusal of the limiter.
func TestDevCodeEarlyFailureWritesNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		refuse bool
		body   func(code string) string
	}{
		{name: "wrong code", refuse: false, body: func(string) string { return codeBody("not-the-code") }},
		{name: "no body", refuse: false, body: func(string) string { return "" }},
		{name: "limiter refuses", refuse: true, body: codeBody},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newDevEnv(t, devEnvOptions{
				authz: nil, kind: installation.KindDev, noCode: false, noAccess: false, noAdmin: false,
				grantFails: false,
			})
			env.limiter.refuse = tt.refuse

			_, grantsBefore := env.devWrites(t)

			if rec := env.post(t, tt.body(env.code)); rec.Code < http.StatusBadRequest {
				t.Fatalf("status = %d, want a refusal", rec.Code)
			}

			if exists, grants := env.devWrites(t); exists || grants != grantsBefore {
				t.Fatalf("person exists = %v, grants = %d (was %d), want nothing written", exists, grants, grantsBefore)
			}
		})
	}
}

// TestProductionRefusesDevSession proves the layer of authentication: the
// session that a dev sign-in made, on a database that a production handler
// serves later (a restored dev backup), authenticates nothing. The dev
// handler accepts the same cookie.
func TestProductionRefusesDevSession(t *testing.T) {
	t.Parallel()

	env := newDevEnv(t, devEnvOptions{
		authz: nil, kind: installation.KindDev, noCode: false, noAccess: false, noAdmin: false, grantFails: false,
	})

	cookie := cookieOf(env.post(t, codeBody(env.code)), apiserver.SessionCookie)
	if cookie == nil {
		t.Fatal("the dev sign-in set no session cookie")
	}

	if rec := env.get(t, "/api/server/principals", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("dev handler with its own session: status = %d, want 200: %s", rec.Code, rec.Body)
	}

	for _, kind := range []installation.Kind{"", installation.KindProduction} {
		prod := env.rebuild(devParts{
			kind:       kind,
			code:       nil,
			noAccess:   false,
			noAdmin:    false,
			grantFails: false,
			noCode:     true,
			stateWrite: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		})

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/principals", http.NoBody)
		req.AddCookie(cookie)

		rec := httptest.NewRecorder()
		prod.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("kind %q with a dev session: status = %d, want 401: %s", kind, rec.Code, rec.Body)
		}
	}
}
