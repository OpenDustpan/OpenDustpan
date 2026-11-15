package dustpan_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/storefault"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// The two gauges that report the blob purge backlog.
const (
	blobPurgePendingMetric = "dustpan_blob_purge_pending"
	blobPurgeOldestMetric  = "dustpan_blob_purge_oldest_age_seconds"
)

// errWrapperCountsPurges reports a wrapper that still counts the blob purge
// backlog, so the negative row cannot pass by accident.
var errWrapperCountsPurges = errors.New("the wrapper counts the blob purge backlog")

// noPurgeCountReader is an inventory reader that hides the blob purge
// counter of the store behind the interfaces that the server asserts on the
// reader. It embeds every inventory interface that the server needs at
// start-up, and never inventory.BlobPurgeCounter.
type noPurgeCountReader struct {
	inventory.Reader
	inventory.DeletedReader
	inventory.ExpiredDeleted
	inventory.BlobPurges
	inventory.SweepLocker
}

// The wrapper implements the interfaces that the server asserts on the
// inventory reader.
var (
	_ inventory.Reader         = (*noPurgeCountReader)(nil)
	_ inventory.DeletedReader  = (*noPurgeCountReader)(nil)
	_ inventory.ExpiredDeleted = (*noPurgeCountReader)(nil)
	_ inventory.BlobPurges     = (*noPurgeCountReader)(nil)
	_ inventory.SweepLocker    = (*noPurgeCountReader)(nil)
)

// TestBlobPurgeGaugesScrape proves that the metrics route serves the two
// blob purge gauges when the inventory reader counts the blob purge backlog,
// and serves neither of them when it does not. The readers that count are
// the SQLite store of the server and the same store in the fault wrapper.
func TestBlobPurgeGaugesScrape(t *testing.T) {
	t.Parallel()

	now := func() time.Time {
		return time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)
	}

	cases := []struct {
		name  string
		build func(*sqlite.Store) (inventory.Reader, error)
		want  bool
	}{
		{
			name: "the store of the server",
			build: func(st *sqlite.Store) (inventory.Reader, error) {
				return st, nil
			},
			want: true,
		},
		{
			name: "the fault wrapper",
			build: func(st *sqlite.Store) (inventory.Reader, error) {
				return storefault.New(st)
			},
			want: true,
		},
		{
			name: "an inventory reader that does not count the blob purge backlog",
			build: func(st *sqlite.Store) (inventory.Reader, error) {
				var reader inventory.Reader = noPurgeCountReader{
					Reader:         st,
					DeletedReader:  st,
					ExpiredDeleted: st,
					BlobPurges:     st,
					SweepLocker:    st,
				}

				if _, ok := reader.(inventory.BlobPurgeCounter); ok {
					return nil, errWrapperCountsPurges
				}

				return reader, nil
			},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := blobPurgeMetricsBody(t, startBlobPurgeMetricsServer(t, tc.build, now))

			for _, name := range []string{blobPurgePendingMetric, blobPurgeOldestMetric} {
				if got := strings.Contains(body, name); got != tc.want {
					t.Errorf("metrics contain %q: got %v, want %v:\n%s", name, got, tc.want, body)
				}
			}
		})
	}
}

// startBlobPurgeMetricsServer starts a server over the SQLite store of the
// test helpers, with the inventory reader that build returns for that store,
// and answers its handler.
func startBlobPurgeMetricsServer(
	t *testing.T, build func(*sqlite.Store) (inventory.Reader, error), now func() time.Time,
) http.Handler {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)

	reader, err := build(st)
	if err != nil {
		t.Fatalf("build the inventory reader: %v", err)
	}

	opts := append(
		fullServerOptions(t, st, blobs, authn),
		dustpan.WithInventoryReader(reader),
		dustpan.WithClock(now),
	)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return startedHandler(t, srv)
}

// blobPurgeMetricsBody sends GET /api/server/metrics with the admin token
// through h and answers the body.
func blobPurgeMetricsBody(t *testing.T, h http.Handler) string {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/metrics", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/server/metrics: status %d, want 200", rec.Code)
	}

	return rec.Body.String()
}
