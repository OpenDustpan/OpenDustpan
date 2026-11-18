package dustpan

import (
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/metrics"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

func newLabelTestServer(t *testing.T) *Server {
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

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	authn, err := identity.NewStaticToken("admin", "admin-token-for-adapter-label-test-0123456789")
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	srv, err := NewServer(
		WithInstallation("test"),
		WithNamespaceStore(st), WithStackStore(st), WithUpdateStore(st),
		WithCheckpointStore(st), WithEventStore(st), WithSecretKeyStore(st),
		WithSecretWrapper(wrapper), WithWorkspaceStore(st), WithStateStore(st),
		WithBlobStore(blobs), WithInventoryReader(st), WithAuthenticator(authn),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv
}

// adapterLabelsByName checks that each pattern of each adapter has a label,
// and that all patterns of one adapter share it.
func adapterLabelsByName(t *testing.T, labels map[string]string, sources []RouteSource) map[string]string {
	t.Helper()

	byName := map[string]string{}

	for _, source := range sources {
		for _, pattern := range source.Patterns {
			got, ok := labels[pattern]
			if !ok {
				t.Fatalf("pattern %q of adapter %q has no label", pattern, source.Name)
			}

			if prev, seen := byName[source.Name]; seen && prev != got {
				t.Fatalf("adapter %q has labels %q and %q", source.Name, prev, got)
			}

			byName[source.Name] = got
		}
	}

	return byName
}

func TestAdapterLabelFollowsRegistration(t *testing.T) {
	t.Parallel()

	srv := newLabelTestServer(t)
	labels := srv.mounted.adapterLabels()
	sources := srv.RouteSources()

	if len(labels) == 0 {
		t.Fatal("adapter labels map is empty")
	}

	if len(sources) == 0 {
		t.Fatal("route sources are empty")
	}

	byName := adapterLabelsByName(t, labels, sources)

	wantAdapter := map[string]string{
		"pulumi":    metrics.AdapterPulumi,
		"tfe":       metrics.AdapterOpenTofu,
		"tofuhttp":  metrics.AdapterOpenTofu,
		"inventory": metrics.AdapterInventory,
		"server":    metrics.AdapterServer,
		"audit":     metrics.AdapterServer,
		"context":   metrics.AdapterServer,
		"profiles":  metrics.AdapterServer,
		"issuer":    metrics.AdapterServer,
		"sinks":     metrics.AdapterServer,
		"registry":  metrics.AdapterServer,
	}

	for name, want := range wantAdapter {
		if got, ok := byName[name]; ok && got != want {
			t.Errorf("adapter %q label = %q, want %q", name, got, want)
		}
	}

	for pattern, want := range labels {
		if got := adapterLabelForPattern(labels, pattern); got != want {
			t.Errorf("adapterLabelForPattern(%q) = %q, want %q", pattern, got, want)
		}
	}

	for _, pattern := range []string{"", "GET /no/such/pattern"} {
		if got := adapterLabelForPattern(labels, pattern); got != metrics.AdapterServer {
			t.Errorf("adapterLabelForPattern(%q) = %q, want %q", pattern, got, metrics.AdapterServer)
		}
	}
}
