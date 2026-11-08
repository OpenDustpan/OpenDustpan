package dustpan_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const runnerAdminToken = "runner-admin-token-0123456789abcdef"

// runnerServer returns a server over a SQLite store with the namespace acme.
// The public URL is publicURL. Each extra builds an option from the store,
// for example the runner stores.
func runnerServer(
	t *testing.T, publicURL string, extra ...func(st *sqlite.Store) dustpan.Option,
) http.Handler {
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

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Unix(0, 0),
		Name:                "acme",
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err = st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	authn := tokenMapAuthenticator{tokens: map[string]identity.Principal{
		runnerAdminToken: {
			Subject: "operator", DisplayName: "operator", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
			JobUnit: identity.Object{},
		},
	}}

	base := []dustpan.Option{
		dustpan.WithInstallation(installationName), dustpan.WithNamespaceStore(st), dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st), dustpan.WithCheckpointStore(st), dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st), dustpan.WithSecretWrapper(wrapper), dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st), dustpan.WithBlobStore(blobs), dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn), dustpan.WithDurableStore(st.DurableOperations()),
		dustpan.WithPublicURL(publicURL),
	}
	extras := make([]dustpan.Option, 0, len(extra))
	for _, option := range extra {
		extras = append(extras, option(st))
	}

	srv, err := dustpan.NewServer(slices.Concat(base, extras)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return startedHandler(t, srv)
}

// withRunnerStores gives the server the runner stores of the SQLite store.
func withRunnerStores(st *sqlite.Store) dustpan.Option {
	return dustpan.WithRunners(st.Runners())
}

func runnerRequest(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()

	body := strings.NewReader(`{"name":"prod","purpose":"jobs"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, body)
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// TestRunnerRoutesAreMountedWithTheStores proves the server serves the
// management API and the protocol only with runner stores and a public URL.
func TestRunnerRoutesAreMountedWithTheStores(t *testing.T) {
	t.Parallel()

	mounted := runnerServer(t, "https://dustpan.test/", withRunnerStores)

	rec := runnerRequest(t, mounted, "/api/runners/acme/pools", runnerAdminToken)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pool = %d %s, want 201", rec.Code, rec.Body.String())
	}

	rec = runnerRequest(t, mounted, "/runner/v1/poll", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), protocol.ErrorInvalid) {
		t.Fatalf("poll with no release = %d %s, want 400 invalid in the protocol shape", rec.Code, rec.Body.String())
	}

	if rec.Header().Get(protocol.HeaderRelease) == "" {
		t.Errorf("the answer has no %s header", protocol.HeaderRelease)
	}

	for name, h := range map[string]http.Handler{
		"no runner stores": runnerServer(t, "https://dustpan.test"),
		"no public URL":    runnerServer(t, "", withRunnerStores),
	} {
		rec = runnerRequest(t, h, "/runner/v1/poll", "")
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: poll = %d %s, want a refusal of an unserved path", name, rec.Code, rec.Body.String())
		}

		rec = runnerRequest(t, h, "/api/runners/acme/pools", runnerAdminToken)
		if rec.Code == http.StatusCreated {
			t.Errorf("%s: create pool = 201, want no runner API", name)
		}
	}
}
