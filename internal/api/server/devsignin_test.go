package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
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
	handler *apiserver.Handler
	store   *sqlite.Store
	access  *access.Service
	limiter *fakeLimiter
	clock   *envClock
	// code is the text of the one-time code, empty when the handler has none.
	code string
}

// devEnvOptions are the options of newDevEnv. noCode gives the handler no
// one-time code. authz replaces the access authorizer when it is not nil.
type devEnvOptions struct {
	authz  identity.Authorizer
	kind   installation.Kind
	noCode bool
}

func newDevEnv(t *testing.T, opts devEnvOptions) devEnv {
	t.Helper()

	clock := &envClock{mu: sync.Mutex{}, now: time.Now().UTC().Truncate(time.Second)}
	st := openRouteTestStore(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "test"})
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

	var (
		code *devcode.Code
		text string
	)

	if !opts.noCode {
		if code, text, err = devcode.Make(clock.Now); err != nil {
			t.Fatalf("make code: %v", err)
		}
	}

	authz := opts.authz
	if authz == nil {
		authz = grants.Authorizer()
	}

	limiter := &fakeLimiter{mu: sync.Mutex{}, failures: 0, refuse: false}

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:    tokenAuthenticator{},
		Authorizer:       authz,
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           st,
		Journal:          st,
		Principals:       seedRoutePrincipals(t, st),
		Access:           grants,
		Tokens:           seedRouteTokens(t, st),
		Audit:            st,
		IPAllowlists:     nil,
		SessionPolicy:    policies,
		CheckpointEpochs: nil,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     stubSweeper{},
		TempAdmins:       nil,
		Now:              nil,
		AuditSinks:       nil,
		SignIn: &apiserver.SignIn{
			Sessions: sessions, Limiter: limiter, Stored: nil, Admin: nil, Origins: []string{signInBase},
			Providers: nil, DevCode: code,
		},
		InstallationKind: opts.kind,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return devEnv{handler: handler, store: st, access: grants, limiter: limiter, clock: clock, code: text}
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

			env := newDevEnv(t, devEnvOptions{authz: nil, kind: installation.KindDev, noCode: false})

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

	env := newDevEnv(t, devEnvOptions{authz: nil, kind: installation.KindDev, noCode: false})

	first := env.post(t, codeBody(env.code))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first request: status = %d, want 204: %s", first.Code, first.Body)
	}

	cookie := cookieOf(first, apiserver.SessionCookie)
	if cookie == nil {
		t.Fatal("first request set no session cookie")
	}

	checkBrowserCookie(t, apiserver.SessionCookie, cookie)

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

	env := newDevEnv(t, devEnvOptions{authz: nil, kind: installation.KindDev, noCode: false})

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

	env := newDevEnv(t, devEnvOptions{authz: nil, kind: installation.KindDev, noCode: false})
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

			env := newDevEnv(t, devEnvOptions{authz: nil, kind: kind, noCode: true})

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

			env := newDevEnv(t, devEnvOptions{authz: tt.authz, kind: tt.kind, noCode: true})

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

// TestCapabilitiesWithDevSession proves that the browser session of the dev
// sign-in reads the installation kind.
func TestCapabilitiesWithDevSession(t *testing.T) {
	t.Parallel()

	env := newDevEnv(t, devEnvOptions{authz: nil, kind: installation.KindDev, noCode: false})

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
