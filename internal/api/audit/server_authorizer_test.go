package audit_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	apitaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// realTokenAuthenticator signs in the subject the token names, so each test
// principal holds the grants it is seeded with.
type realTokenAuthenticator struct{}

func (realTokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        token,
		DisplayName:    token,
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}, nil
}

// seedRealPrincipal creates a person and, when role is set, grants it on obj.
func seedRealPrincipal(t *testing.T, st *sqlite.Store, subject, role string, obj identity.Object) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)

	prn := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		ID:          "prn-" + subject,
		Subject:     subject,
		DisplayName: "",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
		DisabledAt:  time.Time{},
	}
	if err := st.CreatePrincipal(t.Context(), prn, storetest.ServerChange(at, "principal:"+prn.ID)); err != nil {
		t.Fatalf("seed principal %s: %v", subject, err)
	}

	if role == "" {
		return
	}

	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-" + subject, Role: role, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: prn.ID},
	}

	fence, e := storetest.GrantWrite(t, st, obj, at, "grant:"+grt.ID)
	if err := st.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("seed grant %s: %v", subject, err)
	}
}

// TestServerScopeWithBuiltInRoles proves the built-in roles decide the scope
// @server through the real authorizer. The installation operator may read it.
// The account administrator is refused, even though it holds every data
// action, because the data action of a namespace does not cover the
// installation.
func TestServerScopeWithBuiltInRoles(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, createErr := namespaces.Create(t.Context(), "acme"); createErr != nil {
		t.Fatalf("seed namespace: %v", createErr)
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: st, Callers: st, Collections: nil, Units: st, Principals: st,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	seedRealPrincipal(t, st, "olga", access.RoleInstallationOperator, identity.ServerObject())
	seedRealPrincipal(t, st, "ann", access.RoleAccountAdministrator, identity.ServerObject())
	seedRealPrincipal(t, st, "alice", access.RoleAdmin, identity.NamespaceObject("acme"))
	seedRealPrincipal(t, st, "bob", "", identity.Object{})

	handler, err := apitaudit.NewHandler(apitaudit.Config{
		Namespaces:    namespaces,
		Entries:       st,
		EntryReader:   st,
		Authenticator: realTokenAuthenticator{},
		Authorizer:    authz,
		Sessions:      nil,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("audit handler: %v", err)
	}

	cases := []struct {
		name, token, target string
		want                int
	}{
		{
			name:   "operator reads @server",
			token:  "olga",
			target: "/api/audit/entries?organization=@server",
			want:   http.StatusOK,
		},
		{
			name:   "operator checks @server",
			token:  "olga",
			target: "/api/audit/check?organization=@server",
			want:   http.StatusOK,
		},
		{
			name:   "account administrator refused",
			token:  "ann",
			target: "/api/audit/entries?organization=@server",
			want:   http.StatusForbidden,
		},
		{
			name:   "namespace admin refused",
			token:  "alice",
			target: "/api/audit/entries?organization=@server",
			want:   http.StatusForbidden,
		},
		{
			name:   "no grant refused",
			token:  "bob",
			target: "/api/audit/check?organization=@server",
			want:   http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.target, http.NoBody)
			req.Header.Set("Authorization", "token "+tc.token)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
