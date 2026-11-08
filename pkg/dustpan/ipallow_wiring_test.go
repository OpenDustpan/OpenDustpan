package dustpan_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

func TestIPAllowlistsNeedStore(t *testing.T) {
	t.Parallel()

	st, blobs, authn := requireServerPorts(t)
	full := fullServerOptions(t, st, blobs, authn)
	opts := append(append([]dustpan.Option(nil), full...), dustpan.WithIPAllowlists(true))

	_, err := dustpan.NewServer(opts...)
	if err == nil {
		t.Fatal("NewServer with IP allowlists and no store succeeded")
	}

	if !strings.Contains(err.Error(), "WithIPAllowlistStore") {
		t.Fatalf("NewServer error = %v, want it to name WithIPAllowlistStore", err)
	}
}

// newIPAllowlistServer opens a store with a namespace acme holding the list
// 198.51.100.0/24 and returns a server over it with the bootstrap switch.
func newIPAllowlistServer(t *testing.T, includeBootstrap bool) *dustpan.Server {
	t.Helper()

	srv, _ := newIPAllowlistServerWith(t, includeBootstrap)

	return srv
}

// newIPAllowlistServerWith is newIPAllowlistServer with more options. It also
// returns the store.
func newIPAllowlistServerWith(
	t *testing.T, includeBootstrap bool, extra ...dustpan.Option,
) (*dustpan.Server, *sqlite.Store) {
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

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	ctx := t.Context()
	nsName := namespace.Name("acme")

	created := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC),
		Name:                nsName,
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if createErr := st.CreateNamespace(ctx, created); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	seedCatchAllPlacement(t, st, nsName)

	list, err := ipallow.ParseList([]string{"198.51.100.0/24"})
	if err != nil {
		t.Fatalf("parse list: %v", err)
	}

	entry := audit.Entry{
		At:            time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC),
		Namespace:     nsName,
		Subject:       "alice@example.com",
		IDPSubject:    "",
		ObjectID:      "",
		TokenID:       "",
		Action:        "namespace:set-ip-allowlist",
		Object:        nsName.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "storetest",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}

	fence := namespace.Fence{Namespace: nsName, FailoverVersion: 1}

	if setErr := st.SetNamespaceList(ctx, nsName, list, fence, entry); setErr != nil {
		t.Fatalf("set list: %v", setErr)
	}

	opts := append(signInOptions(t, st),
		dustpan.WithInstallation(installationName),
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
		dustpan.WithIPAllowlistsIncludeBootstrap(includeBootstrap),
		dustpan.WithIPAllowlistStore(st),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)

	srv, err := dustpan.NewServer(append(opts, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	registerServer(srv, st, func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) })

	return srv, st
}

// serveNamespaceRead serves one namespace read request from remote.
func serveNamespaceRead(t *testing.T, srv *dustpan.Server, remote string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/server/namespaces/acme/override-switches",
		nil,
	)
	req.RemoteAddr = remote
	req.Header.Set("Authorization", "Bearer "+adminToken)

	rec := httptest.NewRecorder()
	startedHandler(t, srv).ServeHTTP(rec, req)

	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return rec.Code, string(body)
}

func TestIPAllowlistsGateThroughHandler(t *testing.T) {
	t.Parallel()

	included := newIPAllowlistServer(t, true)

	code, body := serveNamespaceRead(t, included, "192.0.2.10")
	if code != http.StatusForbidden {
		t.Fatalf("outside address status = %d, want %d; body %s", code, http.StatusForbidden, body)
	}

	if strings.Contains(body, "198.51.100.0/24") {
		t.Fatalf("403 body %q leaks the list", body)
	}

	exempt := newIPAllowlistServer(t, false)

	code, _ = serveNamespaceRead(t, exempt, "192.0.2.10")
	if code != http.StatusOK {
		t.Fatalf("exempt bootstrap status = %d, want %d", code, http.StatusOK)
	}

	code, _ = serveNamespaceRead(t, included, "198.51.100.7:1234")
	if code != http.StatusOK {
		t.Fatalf("inside address status = %d, want %d", code, http.StatusOK)
	}
}
