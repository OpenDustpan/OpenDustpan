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
)

const (
	deletedAdminToken  = "deleted-admin-token-0123456789abcdef"
	deletedViewerToken = "deleted-viewer-token-0123456789abcde"
)

// newDeletedScopeServer opens a store with the namespaces acme and other, a
// principal nina holding the viewer role on acme only, and returns a server
// over it with the real access authorizer. The token deletedAdminToken signs
// in an administrator, and deletedViewerToken signs in nina.
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

	authn := tokenMapAuthenticator{tokens: map[string]identity.Principal{
		deletedAdminToken: {
			Subject: "operator", DisplayName: "operator", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
		},
		deletedViewerToken: {
			Subject: "nina", DisplayName: "nina", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
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

// TestDeletedUnitsFollowNamespaceGrant proves, through the assembled server,
// that a principal with unit:read on one namespace lists the deleted units
// of that namespace, and no deleted unit of another namespace.
func TestDeletedUnitsFollowNamespaceGrant(t *testing.T) {
	t.Parallel()

	srv := newDeletedScopeServer(t)

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

	code, body := serveDeletedScope(t, srv, deletedScopeCall{
		method: http.MethodGet, target: "/api/inventory/deleted-units?organization=acme",
		token: deletedViewerToken, body: "",
	})
	if code != http.StatusOK {
		t.Fatalf("list deleted units of acme: %d %s", code, body)
	}

	var page deletedPageWire

	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	if page.Total != 1 || len(page.Units) != 1 || page.Units[0].Namespace != "acme" {
		t.Fatalf("deleted units of acme = %s, want the one unit of acme", body)
	}

	code, body = serveDeletedScope(t, srv, deletedScopeCall{
		method: http.MethodGet, target: "/api/inventory/deleted-units?organization=other",
		token: deletedViewerToken, body: "",
	})
	if code != http.StatusForbidden {
		t.Fatalf("list deleted units of other: %d %s, want 403", code, body)
	}
}
