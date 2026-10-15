//go:build faults

package faults_test

import (
	"net"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	faultRegion = "faults"
	faultToken  = "fault-token-0123456789abcdef0123456789"
	faultNS     = "acme"
)

// faultServer is a running OpenDustpan server on a fresh SQLite database.
type faultServer struct {
	url      string
	upstream string
	store    *sqlite.Store
	token    string
}

// startFaultServer starts an OpenDustpan server the same way the e2e harness
// does: a fresh SQLite store, a directory blob store and static tokens,
// served with httptest. It creates the acme namespace.
func startFaultServer(t *testing.T) *faultServer {
	t.Helper()

	ctx := t.Context()

	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dustpan.db"))
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

	authn, err := identity.NewStaticToken("admin", faultToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(
		dustpan.WithRegion(faultRegion),
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

	ts := httptest.NewUnstartedServer(srv.Handler())
	listenForToxiproxy(t, ts)
	ts.Start()
	t.Cleanup(ts.Close)

	if err := st.CreateNamespace(ctx, namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            namespace.Name(faultNS),
		HomeRegion:      namespace.Region(faultRegion),
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	addr, ok := ts.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("test server address %s is not TCP", ts.Listener.Addr())
	}

	upstream := net.JoinHostPort("127.0.0.1", strconv.Itoa(addr.Port))

	return &faultServer{url: "http://" + upstream, upstream: upstream, store: st, token: faultToken}
}

// listenForToxiproxy makes ts listen on all interfaces when
// TOXIPROXY_UPSTREAM_HOST is set. A Toxiproxy server in a container cannot
// reach the loopback address of the test machine, so it dials that host.
func listenForToxiproxy(t *testing.T, ts *httptest.Server) {
	t.Helper()

	if toxiproxyUpstreamHost() == "" {
		return
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", ":0")
	if err != nil {
		t.Fatalf("listen on all interfaces: %v", err)
	}

	if err := ts.Listener.Close(); err != nil {
		t.Fatalf("close default listener: %v", err)
	}

	ts.Listener = ln
}
