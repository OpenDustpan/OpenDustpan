package dustpan_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

const (
	nsAdminToken   = "ns-admin-token-0123456789abcdef0123"
	instAdminToken = "inst-admin-token-0123456789abcdef01"
)

// tokenMapAuthenticator signs in fixed non-admin principals by token.
type tokenMapAuthenticator struct {
	tokens map[string]identity.Principal
}

// AuthenticateToken implements identity.Authenticator.
func (m tokenMapAuthenticator) AuthenticateToken(
	_ context.Context, token string,
) (identity.Principal, error) {
	if p, ok := m.tokens[token]; ok {
		return p, nil
	}

	return identity.Principal{}, identity.ErrUnauthenticated
}

// storePrincipal stores a person principal with subject.
func storePrincipal(t *testing.T, st *sqlite.Store, subject string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt:   time.Unix(0, 0),
		UpdatedAt:   time.Unix(0, 0),
		ID:          "prn-" + subject,
		Subject:     subject,
		DisplayName: subject,
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "", GroupCount: 0,
		DisabledAt: time.Time{},
	}

	createErr := st.CreatePrincipal(
		t.Context(), p, storetest.ServerChange(time.Unix(0, 0), "principal:prn-"+subject),
	)
	if createErr != nil {
		t.Fatalf("create principal %q: %v", subject, createErr)
	}
}

// storeGrant stores a grant of role on obj to the principal subject, with no
// checks: the test seeds what the access service would allow.
func storeGrant(t *testing.T, st *sqlite.Store, subject, role string, obj identity.Object) {
	t.Helper()

	fence, e := storetest.GrantWrite(t, st, obj, time.Unix(0, 0), "grant:test")

	g := access.Grant{
		CreatedAt: time.Unix(0, 0),
		Condition: nil,
		Object:    obj,
		ID:        "grt-" + subject + "-" + role,
		Role:      role,
		CreatedBy: "admin",
		Subject:   access.Subject{Kind: access.SubjectPrincipal, ID: "prn-" + subject},
	}

	if err := st.CreateGrant(t.Context(), g, fence, e); err != nil {
		t.Fatalf("create grant of %q on %q: %v", role, obj.String(), err)
	}
}

// newIPAllowlistAccessServer opens a store with a namespace acme, a principal
// nsadmin holding the admin role on acme only, and a principal instadmin
// holding the account-administrator role on the installation, and returns a
// server over it with IP allowlists on. The server uses the real access
// authorizer over the store.
func newIPAllowlistAccessServer(t *testing.T) *dustpan.Server {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	acme := namespace.Namespace{
		CreatedAt:       time.Unix(0, 0),
		Name:            "acme",
		HomeRegion:      region,
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if nsErr := st.CreateNamespace(t.Context(), acme); nsErr != nil {
		t.Fatalf("create namespace: %v", nsErr)
	}

	storePrincipal(t, st, "nsadmin")
	storePrincipal(t, st, "instadmin")
	storeGrant(t, st, "nsadmin", access.RoleAdmin, identity.NamespaceObject("acme"))
	storeGrant(t, st, "instadmin", access.RoleAccountAdministrator, identity.ServerObject())

	authn := tokenMapAuthenticator{tokens: map[string]identity.Principal{
		nsAdminToken: {
			Subject:                "nsadmin",
			DisplayName:            "nsadmin",
			Admin:                  false,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
		},
		instAdminToken: {
			Subject:                "instadmin",
			DisplayName:            "instadmin",
			Admin:                  false,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
		},
	}}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(region),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithIPAllowlists(true),
		dustpan.WithIPAllowlistStore(st),
		dustpan.WithClock(func() time.Time { return time.Unix(0, 0).UTC() }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// serveIPAllowlistAccess serves one IP allowlist request with token and
// answers its status and body.
func serveIPAllowlistAccess(
	t *testing.T, srv *dustpan.Server, method, token string,
) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		method,
		"/api/server/namespaces/acme/ip-allowlist",
		strings.NewReader(`{"cidrs":["198.51.100.0/24"]}`),
	)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return rec.Code, string(body)
}

// TestIPAllowlistNeedsInstallationGrant proves the allowlist action lives in
// the administration family: a principal whose only grant is the admin role
// on one namespace gets 403 on PUT and DELETE, while a principal with an
// installation-level administration grant gets 200 on PUT and 204 on DELETE.
func TestIPAllowlistNeedsInstallationGrant(t *testing.T) {
	t.Parallel()

	srv := newIPAllowlistAccessServer(t)

	if code, body := serveIPAllowlistAccess(t, srv, http.MethodPut, nsAdminToken); code != http.StatusForbidden {
		t.Fatalf("namespace admin PUT answers %d, want %d: %s", code, http.StatusForbidden, body)
	}

	if code, body := serveIPAllowlistAccess(t, srv, http.MethodDelete, nsAdminToken); code != http.StatusForbidden {
		t.Fatalf("namespace admin DELETE answers %d, want %d: %s", code, http.StatusForbidden, body)
	}

	if code, body := serveIPAllowlistAccess(t, srv, http.MethodPut, instAdminToken); code != http.StatusOK {
		t.Fatalf("installation admin PUT answers %d, want %d: %s", code, http.StatusOK, body)
	}

	if code, body := serveIPAllowlistAccess(t, srv, http.MethodDelete, instAdminToken); code != http.StatusNoContent {
		t.Fatalf("installation admin DELETE answers %d, want %d: %s", code, http.StatusNoContent, body)
	}
}
