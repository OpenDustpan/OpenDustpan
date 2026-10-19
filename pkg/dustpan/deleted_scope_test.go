package dustpan_test

import (
	"encoding/json"
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
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

const (
	deletedAdminToken  = "deleted-admin-token-0123456789abcdef"
	deletedViewerToken = "deleted-viewer-token-0123456789abcde"
	deletedReaderToken = "deleted-reader-token-0123456789abcde"
)

// namespaceReaderRole is a custom role that holds namespace:read and no
// unit:read.
const namespaceReaderRole = "rol-namespace-reader"

// newDeletedScopeServer opens a store with the namespaces acme and other, a
// principal nina holding the viewer role on acme only, and a principal rita
// holding namespace:read and no unit:read on other. It returns a server over
// it with the real access authorizer. The token deletedAdminToken signs in an
// administrator, deletedViewerToken signs in nina, and deletedReaderToken
// signs in rita.
func newDeletedScopeServer(t *testing.T) *dustpan.Server {
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

	for _, name := range []namespace.Name{"acme", "other"} {
		ns := namespace.Namespace{
			ID:              namespace.NewID(),
			CreatedAt:       time.Unix(0, 0),
			Name:            name,
			HomeRegion:      region,
			FailoverVersion: 1,
			Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
		}

		if nsErr := st.CreateNamespace(t.Context(), ns); nsErr != nil {
			t.Fatalf("create namespace %s: %v", name, nsErr)
		}
	}

	storePrincipal(t, st, "nina")
	storeGrant(t, st, "nina", access.RoleViewer, identity.NamespaceObject("acme"))

	role := access.Role{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), ID: namespaceReaderRole, Name: "namespace-reader",
		Description: "Reads namespaces only.", Actions: []identity.Action{identity.ActionNamespaceRead}, BuiltIn: false,
	}
	roleErr := st.CreateRole(t.Context(), role, storetest.ServerChange(time.Unix(0, 0), "role:"+role.ID))
	if roleErr != nil {
		t.Fatalf("create role: %v", roleErr)
	}

	storePrincipal(t, st, "rita")
	storeGrant(t, st, "rita", namespaceReaderRole, identity.NamespaceObject("other"))

	authn := tokenMapAuthenticator{tokens: map[string]identity.Principal{
		deletedAdminToken: {
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Subject:                "operator",
			DisplayName:            "operator",
			Admin:                  true,
			TokenID:                "",
			TokenExpiresAt:         time.Time{},
		},
		deletedViewerToken: {
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Subject:                "nina", DisplayName: "nina", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		},
		deletedReaderToken: {
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
			Subject:                "rita", DisplayName: "rita", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
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
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// deletedScopeCall is one request of the deleted-units test.
type deletedScopeCall struct {
	method, target, token, body string
}

// deletedUnitWire is the part of a deleted-unit entry that the test reads.
type deletedUnitWire struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// deletedPageWire is the part of a deleted-units page that the test reads.
type deletedPageWire struct {
	Units []deletedUnitWire `json:"units"`
	Total int               `json:"total"`
}

// serveDeletedScope serves one request and answers its status and body.
func serveDeletedScope(t *testing.T, srv *dustpan.Server, call deletedScopeCall) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), call.method, call.target, strings.NewReader(call.body))
	req.Header.Set("Authorization", "token "+call.token)
	req.Header.Set("Accept", "application/vnd.pulumi+8")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	got, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return rec.Code, string(got)
}

// seedDeletedUnits creates and deletes the stack web/prod in acme and in
// other as the administrator.
func seedDeletedUnits(t *testing.T, srv *dustpan.Server) {
	t.Helper()

	for _, ns := range []string{"acme", "other"} {
		code, body := serveDeletedScope(t, srv, deletedScopeCall{
			method: http.MethodPost, target: "/api/stacks/" + ns + "/web",
			token: deletedAdminToken, body: `{"stackName":"prod"}`,
		})
		if code != http.StatusOK {
			t.Fatalf("create stack in %s: %d %s", ns, code, body)
		}

		code, body = serveDeletedScope(t, srv, deletedScopeCall{
			method: http.MethodDelete, target: "/api/stacks/" + ns + "/web/prod", token: deletedAdminToken, body: "",
		})
		if code != http.StatusNoContent && code != http.StatusOK {
			t.Fatalf("delete stack in %s: %d %s", ns, code, body)
		}
	}
}

// listDeleted lists the deleted units of ns with token. It answers the
// status, the decoded page of a 200 answer and the body.
func listDeleted(t *testing.T, srv *dustpan.Server, token, ns string) (int, deletedPageWire, string) {
	t.Helper()

	var page deletedPageWire

	code, body := serveDeletedScope(t, srv, deletedScopeCall{
		method: http.MethodGet, target: "/api/dustpan/v1/namespaces/" + ns + "/deleted-units", token: token, body: "",
	})
	if code != http.StatusOK {
		return code, page, body
	}

	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	return code, page, body
}

// TestDeletedUnitsFollowNamespaceGrant proves, through the assembled server,
// that a principal with unit:read on one namespace lists the deleted units
// of that namespace, and no deleted unit of another namespace.
func TestDeletedUnitsFollowNamespaceGrant(t *testing.T) {
	t.Parallel()

	srv := newDeletedScopeServer(t)
	seedDeletedUnits(t, srv)

	code, page, body := listDeleted(t, srv, deletedViewerToken, "acme")
	if code != http.StatusOK || page.Total != 1 || len(page.Units) != 1 || page.Units[0].Namespace != "acme" {
		t.Fatalf("deleted units of acme = %d %s, want the one unit of acme", code, body)
	}

	if code, _, body = listDeleted(t, srv, deletedViewerToken, "other"); code != http.StatusForbidden {
		t.Fatalf("list deleted units of other: %d %s, want 403", code, body)
	}
}

// TestDeletedUnitsNeedUnitRead proves, through the assembled server, that a
// principal with namespace:read and no unit:read on a namespace passes the
// route and lists none of its deleted units.
func TestDeletedUnitsNeedUnitRead(t *testing.T) {
	t.Parallel()

	srv := newDeletedScopeServer(t)
	seedDeletedUnits(t, srv)

	code, page, body := listDeleted(t, srv, deletedReaderToken, "other")
	if code != http.StatusOK || page.Total != 0 || len(page.Units) != 0 {
		t.Fatalf("deleted units of other without unit:read = %d %s, want 200 and none", code, body)
	}
}
