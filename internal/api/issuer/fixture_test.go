package issuer_test

import (
	"context"
	"embed"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
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

// packageSources holds every Go file in this package, so a test can prove from
// the sources that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

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

func (a testAuthz) Authorize(_ context.Context, p identity.Principal, action identity.Action, obj identity.Object) error {
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
}

// build options.
type buildOptions struct {
	deny        map[identity.Action]bool
	sessionAge  time.Duration
	withSession bool
	tweak       func(cfg *apiissuer.Config)
	noHost      bool
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	return buildFixture(t, buildOptions{})
}

func buildFixture(t *testing.T, o buildOptions) fixture {
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

	authz := testAuthz{deny: o.deny}

	host := "https://dustpan.example"
	if o.noHost {
		host = ""
	}

	svc, err := issuersvc.NewService(issuersvc.Config{
		Store: store.Issuer(), Namespaces: store, Sealer: secrets, Authorizer: authz, Notifier: nil, HTTP: nil,
		IssuerBaseURL: host, PublicURL: "", Now: nil, Log: nil,
	})
	if err != nil {
		t.Fatalf("issuer service: %v", err)
	}

	cfg := apiissuer.Config{
		Issuer: svc, Authenticator: testAuthn{}, Authorizer: authz, Audit: store, Namespaces: nil,
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

	return fixture{handler: handler, served: apidesctest.CheckStatus(t, handler), store: store, ns: ns}
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
