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
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// coverageSignInRoutes returns the sign-in rows of the installation route
// table. The sign-in needs a principal store and a session store, so the
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
		Store: st, Policies: coverageSessionPolicies(tb), Now: nil,
	})
	if err != nil {
		tb.Fatalf("session service: %v", err)
	}

	handler, err := serverapi.NewHandler(serverapi.Config{
		Authenticator: coverageAuthenticator{}, Authorizer: coverageAuthorizer{}, Logger: slog.New(slog.DiscardHandler),
		Namespaces: coverageNamespaces(tb), Backups: nil, Restores: nil, Maintenance: coverageMaintenance{},
		Metrics: coverageMetrics{}, Version: "test", Schema: coverageSchema{}, Journal: nil, Principals: principals,
		Access: nil, Tokens: nil, Audit: nil, OnAuditFailure: nil, DeletedUnits: nil, AuditSinks: nil,
		IPAllowlists: nil, SessionPolicy: nil,
		SignIn: &serverapi.SignIn{Sessions: sessions, Limiter: nil, Providers: nil, Origins: nil},
	})
	if err != nil {
		tb.Fatalf("server handler with the sign-in: %v", err)
	}

	var out []RouteRef

	for _, ref := range serverRefs(handler.Routes()) {
		if strings.Contains(ref.Path, "/sign-in/") || ref.Path == "/api/server/session" ||
			ref.Path == "/api/server/sign-out" {
			out = append(out, ref)
		}
	}

	return out
}
