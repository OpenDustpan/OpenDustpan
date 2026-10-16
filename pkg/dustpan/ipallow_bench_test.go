package dustpan_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// benchAllowAll is an identity.Authorizer that allows every request. The
// list benchmark wraps it in the same ipallow.Gate chain that the server
// wiring builds, so the loop measures the list check only.
type benchAllowAll struct{}

func (benchAllowAll) Authorize(
	_ context.Context, _ identity.Principal, _ identity.Action, _ identity.Object,
) error {
	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (benchAllowAll) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// newIPAllowlistBenchServer opens a SQLite store holding a namespace acme
// with the list 198.51.100.0/24 and returns a server over it with the store.
// When on is false the server skips the gate: the baseline of the handler
// benchmark.
func newIPAllowlistBenchServer(b *testing.B, on bool) (*dustpan.Server, *sqlite.Store) {
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
		CreatedAt:       time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC),
		Name:            nsName,
		HomeRegion:      region,
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if createErr := st.CreateNamespace(ctx, created); createErr != nil {
		b.Fatalf("create namespace: %v", createErr)
	}

	list, err := ipallow.ParseList([]string{"198.51.100.0/24"})
	if err != nil {
		b.Fatalf("parse list: %v", err)
	}

	entry := audit.Entry{
		At:           time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC),
		Namespace:    nsName,
		Subject:      "alice@example.com",
		IDPSubject:   "",
		ObjectID:     "",
		TokenID:      "",
		Action:       "namespace:set-ip-allowlist",
		Object:       nsName.String(),
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "192.0.2.1",
		UserAgent:    "storetest",
		Channel:      audit.ChannelAPI,
		Kind:         audit.KindChange,
		Outcome:      audit.OutcomeSuccess,
		RequestID:    "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
		FenceVersion: 0,
	}

	fence := namespace.Fence{Namespace: nsName, FailoverVersion: 1}

	if setErr := st.SetNamespaceList(ctx, nsName, list, fence, entry); setErr != nil {
		b.Fatalf("set list: %v", setErr)
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
		dustpan.WithIPAllowlists(on),
		dustpan.WithIPAllowlistsIncludeBootstrap(true),
		dustpan.WithIPAllowlistStore(st),
		dustpan.WithClock(func() time.Time { return time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		b.Fatalf("new server: %v", err)
	}

	return srv, st
}

// serveBenchNamespaceRead serves one namespace read request from an address
// inside the list and returns the status code.
func serveBenchNamespaceRead(b *testing.B, srv *dustpan.Server) int {
	b.Helper()

	req := httptest.NewRequestWithContext(
		b.Context(),
		http.MethodGet,
		"/api/server/namespaces/acme/override-switches",
		nil,
	)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Header.Set("Authorization", "Bearer "+adminToken)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	return rec.Code
}

// BenchmarkIPAllowlistServeNamespaceRead serves one namespace read per
// iteration with the feature on and a list on the namespace.
func BenchmarkIPAllowlistServeNamespaceRead(b *testing.B) {
	srv, _ := newIPAllowlistBenchServer(b, true)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if code := serveBenchNamespaceRead(b, srv); code != http.StatusOK {
			b.Fatalf("inside address status = %d, want %d", code, http.StatusOK)
		}
	}
}

// BenchmarkIPAllowlistServeNamespaceReadOff serves one namespace read per
// iteration with the feature off: the baseline of the benchmark above.
func BenchmarkIPAllowlistServeNamespaceReadOff(b *testing.B) {
	srv, _ := newIPAllowlistBenchServer(b, false)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if code := serveBenchNamespaceRead(b, srv); code != http.StatusOK {
			b.Fatalf("inside address status = %d, want %d", code, http.StatusOK)
		}
	}
}

// BenchmarkIPAllowlistAuthorize1000Stacks calls Authorize for 1000 different
// stack objects in the one allowlisted namespace per iteration, as a list
// route does. The gate wraps a stub allow-all Next with Rules over the real
// SQLite store, the same chain the server wiring builds. The ns/op figure is
// the time of one loop of 1000 checks.
func BenchmarkIPAllowlistAuthorize1000Stacks(b *testing.B) {
	_, st := newIPAllowlistBenchServer(b, true)

	nsName := namespace.Name("acme")

	gate := ipallow.Gate{Next: benchAllowAll{}, Rules: st, Enabled: true, IncludeBootstrap: true}

	principal := identity.Principal{
		Subject:        "alice@example.com",
		DisplayName:    "alice@example.com",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}

	objs := make([]identity.Object, 0, 1000)
	for i := range 1000 {
		obj, objErr := identity.UnitObject("pulumi", nsName, fmt.Sprintf("web/stack-%04d", i))
		if objErr != nil {
			b.Fatalf("unit object %d: %v", i, objErr)
		}

		objs = append(objs, obj)
	}

	addrCtx := clientip.WithAddr(b.Context(), netip.MustParseAddr("198.51.100.7"))

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		for i := range objs {
			obj := objs[i]
			if authErr := gate.Authorize(addrCtx, principal, identity.ActionUnitRead, obj); authErr != nil {
				b.Fatalf("Authorize inside the list = %v, want nil", authErr)
			}
		}
	}
}
