//go:build e2e

package e2e

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
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

	handler, err := serverapi.NewHandler(serverapi.Config{
		Preferences:    nil,
		GitHost:        nil,
		Idempotency:    nil,
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
		Tokens:         nil,
		Audit:          coverageAuditStore{},
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		AuditSinks:     nil,
		AuditChain:     nil,
		IPAllowlists:   nil,
		Gates:          nil,
		SessionPolicy:  nil,
		SignIn: &serverapi.SignIn{
			OnDevRefused: nil,
			Sessions:     sessions, Limiter: nil, Stored: nil, Admin: admin, Providers: nil, Origins: nil,
			DevCode: code, DevAdmin: identity.Principal{}, //nolint:exhaustruct_v5 // The coverage test calls no route.
		},
		CheckpointEpochs: nil,
		InstallationKind: installation.KindDev,
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
		Store:      st, Guard: principal.AdminOnlyGuard{}, Namespaces: nil, Tokens: nil, Now: nil, NewID: nil,
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

// signInRows keeps the sign-in rows of the route table of handler.
func signInRows(handler *serverapi.Handler) []RouteRef {
	var out []RouteRef

	for _, ref := range serverRefs(handler.Routes()) {
		if strings.Contains(ref.Path, "/sign-in/") || strings.HasPrefix(ref.Path, "/api/server/sign-in-providers") ||
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
