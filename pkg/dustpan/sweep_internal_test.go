package dustpan

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/internal/placementtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/blobtest"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// sweepClock is a clock that a test moves forward.
type sweepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *sweepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *sweepClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// sweepFixture is the services of a server over SQLite and a file blob
// store, with a retention of one hour and a clock that the test moves.
type sweepFixture struct {
	store    *sqlite.Store
	blobs    blob.Store
	clock    *sweepClock
	svc      services
	sweeper  *deletedSweeper
	observed *metrics.ServerMetrics
	log      *bytes.Buffer
}

func newSweepFixture(t *testing.T) sweepFixture {
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

	clock := &sweepClock{mu: sync.Mutex{}, now: time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)}
	log := &bytes.Buffer{}

	cfg := defaultSettings()
	for _, opt := range []Option{
		WithInstallation("test"), WithNamespaceStore(st), WithStackStore(st), WithUpdateStore(st),
		WithCheckpointStore(st), WithEventStore(st), WithSecretKeyStore(st), WithSecretWrapper(wrapper),
		WithWorkspaceStore(st), WithStateStore(st), WithBlobStore(blobs), WithInventoryReader(st),
		WithClock(clock.Now), WithSoftDeleteRetention(time.Hour), WithLogger(slog.New(slog.NewTextHandler(log, nil))),
	} {
		opt(cfg)
	}

	// The manual lock service needs an authorizer, which a started server always has.
	cfg.authorizer = identity.AdminOnly{}

	svc, err := newServices(cfg, nil, cfg.authorizer)
	if err != nil {
		t.Fatalf("services: %v", err)
	}

	observed := metrics.NewServerMetrics("test", clock.Now())

	sweeper, err := newDeletedSweeper(svc, cfg, observed)
	if err != nil {
		t.Fatalf("retention sweep: %v", err)
	}

	if _, err := svc.namespaces.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	return sweepFixture{
		store: st, blobs: blobs, clock: clock, svc: svc, sweeper: sweeper, observed: observed, log: log,
	}
}

// sweepDeleteEntry is the audit entry of the delete of the unit object.
func sweepDeleteEntry(object string) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "acme", Subject: "admin", IDPSubject: "", TokenID: "", Action: "unit:manage",
		Object: object, Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "test", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// sweptUnit is one unit of each tool that the test deletes: its stable ID
// and the prefix of its blobs.
type sweptUnit struct {
	id     string
	prefix string
}

// deleteUnits creates and deletes one stack and one workspace in acme, and
// writes one blob under the prefix of each, as a checkpoint and a state
// would.
func (f sweepFixture) deleteUnits(t *testing.T) []sweptUnit {
	t.Helper()

	ctx := t.Context()

	ref := stack.Ref{Namespace: "acme", Project: "web", Stack: "prod"}

	ns, err := f.svc.namespaces.Get(ctx, "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	st, err := f.svc.stacks.CreatePlaced(ctx, ref, nil, placementtest.Stack(t, f.store, ns.Fence()))
	if err != nil {
		t.Fatalf("create stack: %v", err)
	}

	wsRef := workspace.Ref{Namespace: "acme", Name: "prod"}

	ws, err := f.svc.workspaces.Create(ctx, wsRef, placementtest.Workspace(t, f.store, ns.Fence()))
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	units := []sweptUnit{
		{id: st.ID.String(), prefix: "namespaces/" + ns.ID.String() + "/pulumi/" + st.ID.String() + "/"},
		{id: ws.ID.String(), prefix: "namespaces/" + ns.ID.String() + "/tofu/" + ws.ID.String() + "/"},
	}

	for _, u := range units {
		f.putBlob(ctx, t, u.prefix+"first")
	}

	if err := f.svc.stacks.Delete(ctx, ref, sweepDeleteEntry("pulumi:acme/web/prod")); err != nil {
		t.Fatalf("delete stack: %v", err)
	}

	if err := f.svc.workspaces.Delete(ctx, wsRef, sweepDeleteEntry("opentofu:acme/prod")); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	return units
}

func (f sweepFixture) putBlob(ctx context.Context, t *testing.T, key string) {
	t.Helper()

	if err := f.blobs.Put(ctx, blob.Key(key), strings.NewReader("state")); err != nil {
		t.Fatalf("put blob %s: %v", key, err)
	}
}

func (f sweepFixture) blobCount(t *testing.T, prefix string) int {
	t.Helper()

	listed, err := blobtest.ListAll(t.Context(), f.blobs, prefix)
	if err != nil {
		t.Fatalf("list blobs %s: %v", prefix, err)
	}

	return len(listed)
}

func (f sweepFixture) sweep(t *testing.T) inventory.SweepResult {
	t.Helper()

	res, err := f.sweeper.Sweep(t.Context(), scheduledSweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	return res
}

// TestRetentionSweepRemovesALateBlob proves the retention sweep of both
// tools over a real store: a sweep inside the retention purges nothing; the
// sweep after the retention purges each unit and keeps its blob purge
// record; a blob that a late write leaves after that purge stays while the
// record is inside BlobPurgeGrace; and the first sweep after the grace
// period deletes the late blob and finishes the record.
func TestRetentionSweepRemovesALateBlob(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	units := f.deleteUnits(t)

	f.checkSweep(t, "inside the retention", sweepCounts{purged: 0, finished: 0, pending: 0})

	f.clock.advance(2 * time.Hour)
	f.sweeper.logDue(t.Context())

	if !strings.Contains(f.log.String(), "units_due=2") {
		t.Fatalf("log misses the count of units due:\n%s", f.log.String())
	}

	f.checkSweep(t, "after the retention", sweepCounts{purged: 2, finished: 0, pending: 2})
	checkPurgeLog(t, f.log.String())
	f.checkBlobs(t, units, 0, "after the purge")

	// A save or an upload that passed its checks before the purge writes its
	// blob now, and its SQL commit fails.
	for _, u := range units {
		f.putBlob(t.Context(), t, u.prefix+"late")
	}

	f.clock.advance(inventory.BlobPurgeGrace - time.Hour)
	f.checkSweep(t, "inside the grace period", sweepCounts{purged: 0, finished: 0, pending: 2})
	f.checkBlobs(t, units, 1, "inside the grace period")

	f.clock.advance(2 * time.Hour)
	f.checkSweep(t, "after the grace period", sweepCounts{purged: 0, finished: 2, pending: 0})
	f.checkBlobs(t, units, 0, "after the grace period")

	records, err := f.store.ListBlobPurges(t.Context(), nil, 10)
	if err != nil || len(records) != 0 {
		t.Fatalf("blob purge records = %+v, %v, want none", records, err)
	}

	checkSweepAudit(t, f, units)
	checkSweepMetrics(t, f.observed)
}

// checkPurgeLog checks the INFO line of the sweep that purged both units:
// the count, and the oldest purge time, one hour after the deletes.
func checkPurgeLog(t *testing.T, logged string) {
	t.Helper()

	for _, want := range []string{
		"retention sweep purged deleted units", "purged=2", "oldest_purge_after=2026-11-01T13:00:00",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log misses %q:\n%s", want, logged)
		}
	}
}

// sweepCounts is the expected outcome of one sweep.
type sweepCounts struct {
	purged   int
	finished int
	pending  int
}

// checkSweep runs one sweep and checks its outcome against want, with no
// failure.
func (f sweepFixture) checkSweep(t *testing.T, when string, want sweepCounts) {
	t.Helper()

	res := f.sweep(t)

	got := sweepCounts{purged: len(res.Purged), finished: res.FinishedBlobPurges, pending: res.PendingBlobPurges}
	if got != want || res.Failed != 0 {
		t.Fatalf("sweep %s = %+v (%d failed), want %+v", when, got, res.Failed, want)
	}
}

// checkBlobs checks that each unit has want blobs under its prefix.
func (f sweepFixture) checkBlobs(t *testing.T, units []sweptUnit, want int, when string) {
	t.Helper()

	for _, u := range units {
		if n := f.blobCount(t, u.prefix); n != want {
			t.Fatalf("blobs of %s %s = %d, want %d", u.id, when, n, want)
		}
	}
}

// checkSweepAudit checks that each purge of the sweep wrote one audit entry
// in acme with the server subject, the action unit:purge and the stable ID
// of its unit.
func checkSweepAudit(t *testing.T, f sweepFixture, units []sweptUnit) {
	t.Helper()

	listing, err := f.store.List(t.Context(), "acme", audit.Page{Number: 1, Size: 100})
	if err != nil {
		t.Fatalf("audit entries: %v", err)
	}

	for _, u := range units {
		found := 0

		for i := range listing.Entries {
			e := &listing.Entries[i]
			if e.Action == "unit:purge" && e.ObjectID == u.id && e.Subject == scheduledSweepEntry().Subject {
				found++
			}
		}

		if found != 1 {
			t.Fatalf("purge entries of %s = %d, want 1 in %+v", u.id, found, listing.Entries)
		}
	}
}

// checkSweepMetrics checks the outcome series of the sweep.
func checkSweepMetrics(t *testing.T, observed *metrics.ServerMetrics) {
	t.Helper()

	var out strings.Builder
	if err := observed.Write(&out); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	for _, want := range []string{
		`dustpan_deleted_unit_sweep_total{result="purged"} 2`,
		`dustpan_deleted_unit_sweep_total{result="finished"} 2`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("metrics miss %q:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), `result="failed"`) {
		t.Fatalf("metrics hold a failure:\n%s", out.String())
	}
}

// plainReader is an inventory.Reader that keeps no deleted units, so it
// offers neither port of the retention sweep.
type plainReader struct{}

func (plainReader) ListUnits(context.Context, namespace.Name) ([]inventory.Entry, error) {
	return []inventory.Entry{}, nil
}

func (plainReader) GetUnit(context.Context, string) (inventory.Entry, error) {
	return inventory.Entry{}, inventory.ErrUnitNotFound
}

func (plainReader) QueryUnits(context.Context, inventory.UnitQuery) ([]inventory.Entry, error) {
	return []inventory.Entry{}, nil
}

// TestRetentionSweepNeedsItsPorts proves that a server whose inventory
// reader cannot list the expired units or keep the blob purge records does
// not start, so that no server keeps deleted units that it never purges.
func TestRetentionSweepNeedsItsPorts(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)

	cfg := defaultSettings()
	cfg.inventoryReader = plainReader{}

	if _, err := newDeletedSweeper(f.svc, cfg, f.observed); !errors.Is(err, ErrMissingOption) {
		t.Fatalf("sweep over a plain reader: err = %v, want ErrMissingOption", err)
	}
}
