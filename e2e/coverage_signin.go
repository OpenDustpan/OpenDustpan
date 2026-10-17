//go:build e2e

package e2e

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	serverapi "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// coverageSignInRoutes returns the sign-in rows of the installation route
// table, with the routes of the sign-in providers of the API. The sign-in needs a principal store and a session store,
// so the
// table comes from a handler over a temporary SQLite store.
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

	principals, err := principal.NewService(principal.Config{
		Store: st, Guard: nil, Namespaces: nil, Tokens: nil, Now: nil, NewID: nil,
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

	admin := coverageProviderAdmin(tb, st)

	handler, err := serverapi.NewHandler(serverapi.Config{
		Authenticator: coverageAuthenticator{}, Authorizer: coverageAuthorizer{}, Logger: slog.New(slog.DiscardHandler),
		Namespaces: coverageNamespaces(tb), Backups: nil, Restores: nil, Maintenance: coverageMaintenance{},
		Metrics: coverageMetrics{}, Version: "test", Schema: coverageSchema{}, Journal: nil, Principals: principals,
		Access: nil, Tokens: nil, Audit: nil, OnAuditFailure: nil, DeletedUnits: nil, AuditSinks: nil, AuditChain: nil,
		IPAllowlists: nil, SessionPolicy: nil,
		SignIn: &serverapi.SignIn{
			Sessions: sessions, Limiter: nil, Stored: nil, Admin: admin, Providers: nil, Origins: nil,
		},
		CheckpointEpochs: nil,
	})
	if err != nil {
		tb.Fatalf("server handler with the sign-in: %v", err)
	}

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
