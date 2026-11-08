//go:build e2e

package e2e

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/internal/mfasvc"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/mfa/gowebauthn"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// coverageSignInRoutes returns the sign-in rows of the installation route
// table, with the routes of the sign-in providers of the API. The sign-in needs
// a principal store and a session store, so the table comes from a handler over
// a temporary SQLite store.
func coverageSignInRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	// The route coverage check runs in a cleanup, after the context of the
	// test ends.
	st, err := sqlite.Open(context.WithoutCancel(tb.Context()), filepath.Join(tb.TempDir(), "coverage.db"))
	if err != nil {
		tb.Fatalf("open store: %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			tb.Errorf("close store: %v", closeErr)
		}
	})

	principals, sessions := coverageSignInServices(tb, st)

	admin := coverageProviderAdmin(tb, st)

	// A one-time code mounts the dev sign-in route, so the route table holds it.
	code, _, err := devcode.Make(nil)
	if err != nil {
		tb.Fatalf("make dev sign-in code: %v", err)
	}

	handler, err := serverapi.NewHandler(&serverapi.Config{
		LocalUsers:     coverageLocalUsers(tb, st, principals),
		Preferences:    nil,
		GitHostLinks:   nil,
		Settings:       nil,
		GitHost:        nil,
		Idempotency:    nil,
		Instances:      nil,
		StoreChecks:    nil,
		Workers:        nil,
		TempAdmins:     nil,
		Now:            nil,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     coverageNamespaces(tb),
		Backups:        nil,
		Restores:       nil,
		Maintenance:    coverageMaintenance{},
		Metrics:        coverageMetrics{},
		Version:        "test",
		Schema:         coverageSchema{},
		Journal:        nil,
		Principals:     principals,
		Access:         nil,
		Reach:          coverageReach(),
		Tokens:         nil,
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		AuditSinks:     nil,
		AuditChain:     nil,
		IPAllowlists:   nil,
		Gates:          nil,
		SessionPolicy:  nil,
		SignIn: coverageSignIn(
			coverageSignInService(tb, st, principals, sessions), sessions, admin, code, coveragePasskeys(tb, st),
		),
		CheckpointEpochs: nil,
		InstallationKind: installation.KindDev,
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		DenialLimiter:    nil,
	})
	if err != nil {
		tb.Fatalf("server handler with the sign-in: %v", err)
	}

	return signInRows(handler)
}

// coverageSignInServices returns the principal service and the session service
// over st.
func coverageSignInServices(tb testing.TB, st *sqlite.Store) (*principal.Service, *session.Sessions) {
	tb.Helper()

	principals, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st, Guard: principal.AdminOnlyGuard{}, Namespaces: nil, Tokens: nil, GroupReach: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		tb.Fatalf("principal service: %v", err)
	}

	sessions, err := session.NewSessions(session.SessionsConfig{
		Store: st, Providers: nil, Inactivity: nil, Now: nil,
	})
	if err != nil {
		tb.Fatalf("session service: %v", err)
	}

	return principals, sessions
}

// coverageLocalUsers returns the local user service over st, so the route
// table holds the local user routes and the password sign-in.
func coverageLocalUsers(tb testing.TB, st *sqlite.Store, principals *principal.Service) *localusersvc.Service {
	tb.Helper()

	budget, err := argon2id.NewBudget(localuser.DefaultHashMemoryBudgetKiB)
	if err != nil {
		tb.Fatalf("hash budget: %v", err)
	}

	hasher, err := argon2id.New(budget)
	if err != nil {
		tb.Fatalf("hasher: %v", err)
	}

	svc, err := localusersvc.New(localusersvc.Config{
		Log:        nil,
		Store:      st,
		Principals: principals,
		Hasher:     hasher,
		Blocklist:  blocklist.Embedded(),
		Audit:      st,
		Locks:      st,
		Keys:       st,
		Settings:   nil,
		SSO:        nil,
		Reach: access.ReachConfig{
			Grants: st, Roles: st, Callers: st, Principals: st, Collections: nil, Units: st,
		},
		Subjects: st,
		Kind:     installation.KindDev,
		Now:      nil,
	})
	if err != nil {
		tb.Fatalf("local user service: %v", err)
	}

	return svc
}

// coveragePasskeys returns the passkey service over st, so the route table
// holds the passkey routes.
func coveragePasskeys(tb testing.TB, st *sqlite.Store) *mfasvc.Service {
	tb.Helper()

	rp, err := gowebauthn.New("https://dustpan.example.com")
	if err != nil {
		tb.Fatalf("relying party: %v", err)
	}

	svc, err := mfasvc.New(mfasvc.Config{Store: st, RP: rp, Audit: st, Now: nil, Log: nil})
	if err != nil {
		tb.Fatalf("passkey service: %v", err)
	}

	return svc
}

// signInRows keeps the sign-in rows of the route table of handler.
func signInRows(handler *serverapi.Handler) []RouteRef {
	var out []RouteRef

	for _, ref := range serverRefs(handler.Routes()) {
		if strings.Contains(ref.Path, "/sign-in/") || strings.HasPrefix(ref.Path, "/api/server/sign-in-providers") ||
			strings.HasPrefix(ref.Path, "/api/dustpan/v1/local-users") || ref.Path == "/api/dustpan/v1/me/password" ||
			strings.HasPrefix(ref.Path, "/api/dustpan/v1/me/passkeys") ||
			ref.Path == "/api/server/session" ||
			ref.Path == "/api/server/sign-out" {
			out = append(out, ref)
		}
	}

	return out
}

// coverageProviderAdmin returns a service of the sign-in providers of the
// API over st, so the handler mounts the provider routes.
func coverageProviderAdmin(tb testing.TB, st *sqlite.Store) *session.ProviderAdmin {
	tb.Helper()

	secrets, err := secret.NewInstallationSecrets(secret.InstallationConfig{Keys: st, Wrapper: nil, Now: nil})
	if err != nil {
		tb.Fatalf("installation secrets: %v", err)
	}

	admin, err := session.NewProviderAdmin(session.ProviderAdminConfig{
		Store: st, Binder: st, Secrets: secrets, Now: nil, Static: nil,
	})
	if err != nil {
		tb.Fatalf("sign-in provider service: %v", err)
	}

	return admin
}

// coveragePasswordLimiter is the password budget of the coverage handler:
// it admits each request and counts nothing.
type coveragePasswordLimiter struct{}

func (coveragePasswordLimiter) Admit(context.Context) error { return nil }

func (coveragePasswordLimiter) Fail(context.Context) {}

// coverageSignInService returns the sign-in service over st.
func coverageSignInService(
	tb testing.TB, st *sqlite.Store, principals *principal.Service, sessions *session.Sessions,
) *signinsvc.Service {
	tb.Helper()

	service, err := signinsvc.New(signinsvc.Config{
		Principals: principals, Disabler: st, Generations: st, Sessions: sessions, Audit: st,
		TempAdmins: st, Groups: nil,
		Removals: nil, States: st, Locks: st, NameKeys: st, Logger: slog.New(slog.DiscardHandler),
		OnAuditFailure: nil,
	})
	if err != nil {
		tb.Fatalf("sign-in service: %v", err)
	}

	return service
}

// coverageSignIn returns the sign-in of the coverage handler: the dev
// sign-in code mounts the dev route, and the password budget mounts the
// password routes.
func coverageSignIn(
	service *signinsvc.Service, sessions *session.Sessions, admin *session.ProviderAdmin, code *devcode.Code,
	passkeys *mfasvc.Service,
) *serverapi.SignIn {
	return &serverapi.SignIn{
		Service:         service,
		OnDevRefused:    nil,
		Sessions:        sessions,
		Limiter:         nil,
		Stored:          nil,
		Admin:           admin,
		Providers:       nil,
		Origins:         nil,
		PasswordLimiter: coveragePasswordLimiter{},
		Passkeys:        passkeys,
		PasskeySignIn:   passkeys,
		// The coverage test calls no route, and the route table holds the routes of the passkey sign-in only with a
		// budget for the starts and a budget for the failures.
		PasskeyBeginLimiter: coveragePasswordLimiter{},
		PasskeyLimiter:      coveragePasswordLimiter{},
		DevCode:             code,
		DevAdmin:            identity.Principal{}, //nolint:exhaustruct_v5 // The coverage test calls no route.
	}
}

// coverageSessionPolicyStore holds the default session policy: the route
// table needs a session policy service, and the coverage gate never serves
// a request.
type coverageSessionPolicyStore struct{}

func (coverageSessionPolicyStore) GetSessionPolicy(_ context.Context) (session.Policy, error) {
	return session.DefaultPolicy(), nil
}

func (coverageSessionPolicyStore) UpdateSessionPolicy(
	_ context.Context, _ session.PolicyUpdate,
) (session.Policy, error) {
	return session.DefaultPolicy(), nil
}

// coverageSessionPolicies returns the session policy service of the route
// table.
func coverageSessionPolicies(tb testing.TB) *session.Policies {
	tb.Helper()

	svc, err := session.NewPolicies(session.PolicyConfig{Store: coverageSessionPolicyStore{}, Now: nil})
	if err != nil {
		tb.Fatalf("session policy service: %v", err)
	}

	return svc
}

// devCoveringTest names the test that proves the routes of the dev command
// instead of a CLI: no CLI reads the capabilities document or signs in with
// the code of the first run.
const devCoveringTest = "TestDevCommand"

// devAllowEntries returns the capabilities document and the sign-in with the
// one-time code of the dev command. The steps of devCoveringTest start the
// real binary on a pseudo terminal, play the browser with the code from the
// banner, and read the document with and without the token.
func devAllowEntries() []AllowEntry {
	return []AllowEntry{
		{
			Method: http.MethodGet, Path: "/api/dustpan/v1/capabilities", CoveringTest: devCoveringTest,
			Reason: "Console and client route: no CLI reads the capabilities document. The harness " +
				"step reads it from a dev server with and without the token.",
		},
		{
			Method: http.MethodPost, Path: "/api/dustpan/v1/sign-in/dev-code", CoveringTest: devCoveringTest,
			Reason: "Console route: only the one-time link of the dev command signs in with the code. " +
				"The harness step plays the browser with the code from the banner of the real binary.",
		},
	}
}

// signInAllowEntries returns the console sign-in routes. The steps of
// TestSignIn drive each one in the browser flow against a test OpenID
// provider, on each store.
func signInAllowEntries() []AllowEntry {
	reason := "Console route: no CLI signs in through a browser. " +
		"The harness plays the browser with a cookie jar against a test OpenID provider and checks each answer."

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/server/sign-in/{provider}"},
		{http.MethodGet, "/api/server/sign-in/{provider}/callback"},
		{http.MethodGet, "/api/server/session"},
		{http.MethodPost, "/api/server/sign-out"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestSignIn", Reason: reason,
		})
	}

	return out
}

// signInProviderAllowEntries returns the operator and console routes of
// the sign-in providers. The steps of TestSignIn drive each one through the
// real binary on each store: a create, the list, a read, a change, a
// refused delete and a delete.
func signInProviderAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI manages sign-in providers. " +
		"The harness calls each route through the real binary and checks the answer."

	const list, one = "/api/server/sign-in-providers", "/api/server/sign-in-providers/{id}"

	routes := []struct{ method, path string }{
		{http.MethodGet, list},
		{http.MethodPost, list},
		{http.MethodGet, one},
		{http.MethodPatch, one},
		{http.MethodDelete, one},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestSignIn", Reason: reason,
		})
	}

	return out
}

// sessionPolicyAllowEntries returns the operator and console routes of the
// session policy. The harness steps of TestSessionPolicy drive each one
// through the real binary on each store.
func sessionPolicyAllowEntries() []AllowEntry {
	reason := "Operator and console route: no CLI reads or changes the session policy. " +
		"The harness step calls it through the real binary and checks the answer."

	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/server/session-policy"},
		{http.MethodPatch, "/api/server/session-policy"},
	}

	out := make([]AllowEntry, 0, len(routes))
	for _, r := range routes {
		out = append(out, AllowEntry{
			Method: r.method, Path: r.path, CoveringTest: "TestSessionPolicy", Reason: reason,
		})
	}

	return out
}
