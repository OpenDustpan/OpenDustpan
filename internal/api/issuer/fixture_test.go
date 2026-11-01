package issuer_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	apiissuer "opendustpan.org/opendustpan/internal/api/issuer"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	manage = identity.ActionNamespaceManageIssuer
	read   = identity.ActionNamespaceRead

	issuerPath = "/api/dustpan/v1/namespaces/acme/issuer"
)

// grantsOf returns the actions of a subject. The token of a subject is its
// name.
func grantsOf(subject string) []identity.Action {
	switch subject {
	case "admin":
		return []identity.Action{read, manage}
	case "reader":
		return []identity.Action{read}
	case "manageonly":
		return []identity.Action{manage}
	default:
		return nil
	}
}

type testAuthn struct{}

func (testAuthn) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if grantsOf(token) == nil {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: token, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}, nil
}

// testAuthz allows the actions of the subject, minus the denied ones.
type testAuthz struct{ deny map[identity.Action]bool }

func (a testAuthz) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if slices.Contains(grantsOf(p.Subject), action) && !a.deny[action] {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

func (testAuthz) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// adminSessions accepts every session as the session of admin. The sign-in of
// the session is age old.
type adminSessions struct{ age time.Duration }

func (a adminSessions) Authenticate(context.Context, session.Secret) (session.Match, error) {
	return session.Match{
		Session: session.Session{
			CreatedAt: time.Time{}, LastSeenAt: time.Time{}, AuthTime: time.Now().Add(-a.age), EndedAt: time.Time{},
			ID: "ses-1", PrincipalID: "prn-1", Provider: "corp", EndReason: "", SourceIP: "", UserAgent: "",
			Generation: 1,
		},
		Owner: session.Owner{
			ActiveAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-1", Subject: "admin", DisplayName: "Admin",
			Kind: principal.KindPerson, BreakGlass: false,
		},
		Digest:   session.Digest{},
		Policy:   session.DefaultPolicy(),
		Accounts: session.AccountPolicy{Inactivity: 0, DisableAccounts: false},
	}, nil
}

type fixture struct {
	handler *apiissuer.Handler
	// served is the handler that the tests call: it fails a test when a
	// handler answers a success status that the description does not give.
	served http.Handler
	store  *sqlite.Store
	ns     namespace.Namespace
	// skew moves the clock of the issuer forward, in nanoseconds.
	skew *atomic.Int64
}

// advance moves the clock of the issuer forward by d.
func (f fixture) advance(d time.Duration) { f.skew.Add(int64(d)) }

// buildOptions change the fixture.
type buildOptions struct {
	deny        map[identity.Action]bool
	sessionAge  time.Duration
	withSession bool
	tweak       func(cfg *apiissuer.Config)
	noHost      bool
}

// defaults returns the options of a plain fixture.
func defaults() buildOptions {
	return buildOptions{deny: nil, sessionAge: 0, withSession: false, tweak: nil, noHost: false}
}

func (o buildOptions) withoutHost() buildOptions {
	o.noHost = true

	return o
}

func (o buildOptions) session(age time.Duration) buildOptions {
	o.withSession, o.sessionAge = true, age

	return o
}

func (o buildOptions) tweaked(f func(cfg *apiissuer.Config)) buildOptions {
	o.tweak = f

	return o
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	return buildFixture(t, defaults())
}

// stack is the store, the namespace and the secret service of a fixture.
type stack struct {
	store   *sqlite.Store
	secrets *secret.Service
	ns      namespace.Namespace
}

func openStack(t *testing.T) stack {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	ns, err := namespaces.Create(t.Context(), "acme")
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: store, Wrapper: wrapper, Namespaces: namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	return stack{store: store, secrets: secrets, ns: ns}
}

func buildFixture(t *testing.T, o buildOptions) fixture {
	t.Helper()

	st := openStack(t)
	authz := testAuthz{deny: o.deny}

	host := "https://dustpan.example"
	if o.noHost {
		host = ""
	}

	skew := &atomic.Int64{}

	svc, err := issuersvc.NewService(issuersvc.Config{
		Store: st.store.Issuer(), Namespaces: st.store, Sealer: st.secrets, Authorizer: authz, Notifier: nil,
		Fetcher: nil, IssuerBaseURL: host, PublicURL: "",
		Now: func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }, Log: nil,
	})
	if err != nil {
		t.Fatalf("issuer service: %v", err)
	}

	cfg := apiissuer.Config{
		Issuer: svc, Authenticator: testAuthn{}, Authorizer: authz, Audit: st.store, Namespaces: nil,
		OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, SessionPolicy: nil,
		Logger: slog.New(slog.DiscardHandler),
	}

	if o.withSession {
		checker, checkErr := sessionauth.New(adminSessions{age: o.sessionAge}, []string{"https://dustpan.example"})
		if checkErr != nil {
			t.Fatalf("session check: %v", checkErr)
		}

		cfg.Sessions = checker
	}

	if o.tweak != nil {
		o.tweak(&cfg)
	}

	handler, err := apiissuer.NewHandler(cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return fixture{
		handler: handler,
		served:  apidesctest.CheckStatus(t, handler),
		store:   st.store,
		ns:      st.ns,
		skew:    skew,
	}
}

// request is one call of a test.
type request struct {
	// session sends the request with a console session cookie and no
	// Authorization header.
	session bool
	token   string
	method  string
	target  string
	body    string
	headers map[string]string
}

// response is what a call answers.
type response struct {
	code   int
	header http.Header
	body   string
}

func get(token, target string) request {
	return request{session: false, token: token, method: http.MethodGet, target: target, body: "", headers: nil}
}

func send(token, method, target, body string) request {
	return request{session: false, token: token, method: method, target: target, body: body, headers: nil}
}

func (r request) inSession() request {
	r.session = true
	r.token = ""

	return r
}

func (r request) withHeader(key, value string) request {
	headers := map[string]string{key: value}
	maps.Copy(headers, r.headers)

	r.headers = headers

	return r
}

// do sends one request.
func (f fixture) do(t *testing.T, r request) response {
	t.Helper()

	var reader io.Reader = http.NoBody
	if r.body != "" {
		reader = strings.NewReader(r.body)
	}

	req := httptest.NewRequestWithContext(t.Context(), r.method, r.target, reader)
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}

	if r.session {
		cookieSecret, err := session.NewSecret()
		if err != nil {
			t.Fatalf("secret: %v", err)
		}

		req.AddCookie(&http.Cookie{
			Name: sessionauth.Cookie, Value: cookieSecret.Reveal(), Path: "/", Secure: true, HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		req.Header.Set(sessionauth.CSRFHeader, session.CSRFValue(cookieSecret))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}

	for k, v := range r.headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	f.served.ServeHTTP(rec, req)

	return response{code: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

func wantCode(t *testing.T, what string, got response, want int) {
	t.Helper()

	if got.code != want {
		t.Errorf("%s: %d %s, want %d", what, got.code, got.body, want)
	}
}
