package dustpan

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
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
		WithProfileStore(st.Profiles()), WithIssuerStore(st.Issuer()),
		WithRegistry(st.Registry(), st.RegistryKeys()), WithSinkStore(st.NotificationSinks()),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	// Requests answer 503 until Start succeeds, so they never reach the metrics.
	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("start the server: %v", err)
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

// checkRequestAdapter fails when the request count of route holds no sample
// with the adapter label, or a sample with another one.
func checkRequestAdapter(t *testing.T, output, route, adapter string) {
	t.Helper()

	found := false

	for line := range strings.Lines(output) {
		if !strings.HasPrefix(line, "dustpan_http_requests_total{") || !strings.Contains(line, `route="`+route+`"`) {
			continue
		}

		found = true

		if !strings.Contains(line, `adapter="`+adapter+`"`) {
			t.Errorf("route %q has the wrong adapter label, want %q: %s", route, adapter, line)
		}
	}

	if !found {
		t.Errorf("output holds no request count for route %q:\n%s", route, output)
	}
}

func TestAdapterLabelFollowsRegistration(t *testing.T) {
	t.Parallel()

	srv := newLabelTestServer(t)
	labels, err := srv.mounted.adapterLabels(srv.root.labels)
	if err != nil {
		t.Fatalf("adapter labels: %v", err)
	}
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
		got, ok := byName[name]
		if !ok {
			t.Errorf("adapter %q has no route source", name)

			continue
		}

		if got != want {
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

// TestAdapterLabelOnRequest proves a request through the server handler
// records the label of the adapter that registered its route: the backup
// route under the v1 prefix is server, and the inventory route is inventory.
func TestAdapterLabelOnRequest(t *testing.T) {
	t.Parallel()

	srv := newLabelTestServer(t)

	const (
		backupRoute    = "GET /api/dustpan/v1/backups"
		inventoryRoute = "GET /api/dustpan/v1/namespaces/{namespace}/units"
	)

	for _, path := range []string{"/api/dustpan/v1/backups", "/api/dustpan/v1/namespaces/acme/units"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()

		srv.Handler().ServeHTTP(rec, req)
	}

	var out strings.Builder
	if err := srv.Metrics().Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	checkRequestAdapter(t, out.String(), backupRoute, metrics.AdapterServer)
	checkRequestAdapter(t, out.String(), inventoryRoute, metrics.AdapterInventory)
}

// TestInFlightLabelMatchesRequestLabel proves that the gauge label, which the
// middleware takes from the root mux before the handler runs, is the label of
// the request counter for one route of each adapter label.
func TestInFlightLabelMatchesRequestLabel(t *testing.T) {
	t.Parallel()

	srv := newLabelTestServer(t)

	labels, err := srv.mounted.adapterLabels(srv.root.labels)
	if err != nil {
		t.Fatalf("adapter labels: %v", err)
	}

	metered := requestMetrics{observed: nil, root: srv.root, labels: labels}

	tests := []struct {
		path string
		want string
	}{
		{"/api/dustpan/v1/backups", metrics.AdapterServer},
		{"/api/dustpan/v1/namespaces/acme/units", metrics.AdapterInventory},
		{"/api/user", metrics.AdapterPulumi},
		{"/api/v2/ping", metrics.AdapterOpenTofu},
		{"/tofu/state/acme", metrics.AdapterOpenTofu},
	}

	for _, tt := range tests {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)

		if got := metered.startAdapter(req); got != tt.want {
			t.Errorf("in-flight label of %s = %q, want %q", tt.path, got, tt.want)
		}

		srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}

	var out strings.Builder
	if err := srv.Metrics().Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	for _, tt := range tests {
		if !strings.Contains(out.String(), `adapter="`+tt.want+`"`) {
			t.Errorf("request counter has no adapter %q", tt.want)
		}
	}
}

// TestAdapterLabelOfUnservedPath proves that a request that reaches a mounted
// adapter but matches none of its routes has one label: the counter holds the
// label of the gauge, not server. Each case uses its own server, so the
// counter holds that one request only.
func TestAdapterLabelOfUnservedPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{"/api/no/such/pulumi/route", metrics.AdapterPulumi},
		{"/api/v2/no-such", metrics.AdapterOpenTofu},
		{"/tofu/state/acme", metrics.AdapterOpenTofu},
		{"/api/inventory/nope", metrics.AdapterInventory},
		{"/v1/providers/x/y/versions", metrics.AdapterOpenTofu},
		{"/api/dustpan/v1/no-such", metrics.AdapterServer},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			srv := newLabelTestServer(t)
			labels, err := srv.mounted.adapterLabels(srv.root.labels)
			if err != nil {
				t.Fatalf("adapter labels: %v", err)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)
			gauge := requestMetrics{observed: nil, root: srv.root, labels: labels}.startAdapter(req)

			if gauge != tt.want {
				t.Errorf("in-flight label of %s = %q, want %q", tt.path, gauge, tt.want)
			}

			srv.Handler().ServeHTTP(httptest.NewRecorder(), req)

			requireCounterAdapter(t, srv, tt.path, tt.want)
		})
	}
}

// requireCounterAdapter fails when a line of the request counter of srv names
// an adapter other than want.
func requireCounterAdapter(t *testing.T, srv *Server, path, want string) {
	t.Helper()

	var out strings.Builder
	if err := srv.Metrics().Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(line, "dustpan_http_requests_total{") &&
			!strings.Contains(line, `adapter="`+want+`"`) {
			t.Errorf("request counter of %s: %s, want adapter %q", path, line, want)
		}
	}
}

// TestAdapterLabelsRefuseConflict proves that one pattern or one path with
// two labels stops the build of the label map and names the key and both
// adapters. Removing the check makes the first case return a map.
func TestAdapterLabelsRefuseConflict(t *testing.T) {
	t.Parallel()

	handlers := []namedHandler{
		{name: "one", handler: nil, label: metrics.AdapterServer},
		{name: "two", handler: nil, label: metrics.AdapterInventory},
	}

	tests := []struct {
		name    string
		sources []routeSource
		wantErr bool
	}{
		{
			"same path, other method",
			[]routeSource{
				{name: "one", patterns: []string{"GET /api/dustpan/v1/x"}},
				{name: "two", patterns: []string{"POST /api/dustpan/v1/x"}},
			},
			true,
		},
		{
			"same pattern",
			[]routeSource{
				{name: "one", patterns: []string{"GET /a"}},
				{name: "two", patterns: []string{"GET /a"}},
			},
			true,
		},
		{
			"other paths",
			[]routeSource{
				{name: "one", patterns: []string{"GET /a"}},
				{name: "two", patterns: []string{"GET /b"}},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := buildAdapterLabels(tt.sources, handlers, newLabelSet())
			if tt.wantErr != errors.Is(err, errLabelConflict) {
				t.Fatalf("error = %v, want conflict %v", err, tt.wantErr)
			}

			if tt.wantErr {
				requireNames(t, err, `"one"`, `"two"`)
			}
		})
	}
}

func requireNames(t *testing.T, err error, parts ...string) {
	t.Helper()

	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not name adapter %s", err, part)
		}
	}
}

// TestEveryRootPatternHasLabel proves that each pattern and each mounted path
// on the root mux has a label in the map.
func TestEveryRootPatternHasLabel(t *testing.T) {
	t.Parallel()

	srv := newLabelTestServer(t)

	labels, err := srv.mounted.adapterLabels(srv.root.labels)
	if err != nil {
		t.Fatalf("adapter labels: %v", err)
	}

	if len(srv.root.patterns) == 0 {
		t.Fatal("the root mux holds no pattern")
	}

	for _, pattern := range append(slices.Clone(srv.root.patterns), srv.root.mounted...) {
		if _, ok := labels[pattern]; !ok {
			t.Errorf("root mux pattern %q has no label", pattern)
		}
	}
}

// TestRootMuxRefusesLabelConflict proves that one pattern with two labels on
// the root mux stops the build of the label map.
func TestRootMuxRefusesLabelConflict(t *testing.T) {
	t.Parallel()

	mux := newRootMux()
	mux.Handle("/a/", metrics.AdapterServer, http.NotFoundHandler())
	mux.record("/a/", metrics.AdapterPulumi)

	_, err := buildAdapterLabels(nil, nil, mux.labels)
	if !errors.Is(err, errLabelConflict) {
		t.Fatalf("error = %v, want errLabelConflict", err)
	}
}
