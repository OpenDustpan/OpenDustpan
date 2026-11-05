package dustpan_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
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

var _ = context.Background

// newIPAllowlistBenchServer opens a SQLite store holding a namespace acme
// with the list 198.51.100.0/24 and returns a server over it with the store.
// When on is false the server skips the gate: the baseline of the handler
// benchmark.
func perf11Server(b *testing.B, on bool, extra ...dustpan.Option) (*dustpan.Server, *sqlite.Store) {
	b.Helper()

	st, err := sqlite.Open(b.Context(), filepath.Join(b.TempDir(), "dustpan.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(filepath.Join(b.TempDir(), "blobs"))
	if err != nil {
		b.Fatalf("open blob store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			b.Errorf("close blob store: %v", closeErr)
		}
	})

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		b.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		b.Fatalf("key wrapper: %v", err)
	}

	ctx := b.Context()
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
		b.Fatalf("create namespace: %v", createErr)
	}

	list, err := ipallow.ParseList([]string{"198.51.100.0/24"})
	if err != nil {
		b.Fatalf("parse list: %v", err)
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
		b.Fatalf("set list: %v", setErr)
	}

	srv, err := dustpan.NewServer(append(extra,
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
		dustpan.WithIPAllowlists(on),
		dustpan.WithIPAllowlistsIncludeBootstrap(true),
		dustpan.WithIPAllowlistStore(st),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)...)
	if err != nil {
		b.Fatalf("new server: %v", err)
	}

	return srv, st
}


func perf11Serve(b *testing.B, srv *dustpan.Server, peer, xff string) int {
	b.Helper()
	req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/api/server/namespaces/acme/override-switches", nil)
	req.RemoteAddr = peer
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	startedHandler(b, srv).ServeHTTP(rec, req)
	return rec.Code
}

func perf11Run(b *testing.B, on bool, peer, xff string, extra ...dustpan.Option) {
	srv, _ := perf11Server(b, on, extra...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if code := perf11Serve(b, srv, peer, xff); code != http.StatusOK {
			b.Fatalf("status = %d", code)
		}
	}
}

var perf11Proxies = dustpan.WithTrustedProxies(netip.MustParsePrefix("10.0.0.0/8"))

// off, no proxies: the baseline of every request.
func BenchmarkPerf11Baseline(b *testing.B) { perf11Run(b, false, "198.51.100.7:1234", "") }

// allowlist on, no trusted proxy.
func BenchmarkPerf11AllowlistOn(b *testing.B) { perf11Run(b, true, "198.51.100.7:1234", "") }

// allowlist off, trusted proxy set, request through the proxy.
func BenchmarkPerf11ProxyOnly(b *testing.B) {
	perf11Run(b, false, "10.1.2.3:1234", "198.51.100.7", perf11Proxies)
}

// allowlist on, trusted proxy set, request through the proxy.
func BenchmarkPerf11AllowlistAndProxy(b *testing.B) {
	perf11Run(b, true, "10.1.2.3:1234", "198.51.100.7", perf11Proxies)
}
