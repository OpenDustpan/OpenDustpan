package inventory_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// sweepNow is the time of the injected clock of the sweep tests.
func sweepNow() time.Time {
	return time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
}

// errDriver stands for a driver error whose text must not reach the log.
var errDriver = errors.New("pq: connection to 10.0.0.7 refused")

// memExpired is an inventory.ExpiredDeleted over a list, through
// inventory.PageExpired.
type memExpired struct {
	entries []inventory.DeletedEntry
	calls   int
}

func (m *memExpired) ListExpiredDeleted(
	_ context.Context, now time.Time, after *inventory.ExpiredCursor, limit int,
) ([]inventory.DeletedEntry, error) {
	m.calls++

	return inventory.PageExpired(m.entries, now, after, limit), nil
}

// sweepLifecycle is an inventory.Lifecycle that records each purge and
// answers the error that errs holds for the unit ID.
type sweepLifecycle struct {
	mu      sync.Mutex
	errs    map[string]error
	entries []audit.Entry
}

func (l *sweepLifecycle) Restore(context.Context, namespace.Name, string, string, audit.Entry) (string, error) {
	return "", nil
}

func (l *sweepLifecycle) Purge(_ context.Context, _ namespace.Name, unitID string, e audit.Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.errs[unitID]; err != nil {
		return err
	}

	l.entries = append(l.entries, e)

	return nil
}

// memRecords is an inventory.BlobPurges over a list.
type memRecords struct {
	records   []inventory.BlobPurge
	finished  []string
	finishErr error
}

func (m *memRecords) ListBlobPurges(_ context.Context, limit int) ([]inventory.BlobPurge, error) {
	return m.records[:min(limit, len(m.records))], nil
}

func (m *memRecords) FinishBlobPurge(_ context.Context, tool, unitID string) error {
	if m.finishErr != nil {
		return m.finishErr
	}

	m.finished = append(m.finished, tool+"/"+unitID)

	return nil
}

// memBlobs is an inventory.PurgedBlobs that records each delete and answers
// the error that errs holds for the unit ID.
type memBlobs struct {
	errs    map[string]error
	deleted []string
}

func (m *memBlobs) DeletePurgedBlobs(_ context.Context, unitID string) error {
	m.deleted = append(m.deleted, unitID)

	return m.errs[unitID]
}

// sweepFixture holds a Sweeper over in-memory ports and the log it writes.
type sweepFixture struct {
	sweeper   *inventory.Sweeper
	expired   *memExpired
	lifecycle *sweepLifecycle
	records   *memRecords
	blobs     *memBlobs
	log       *bytes.Buffer
}

func newSweepFixture(t *testing.T, entries []inventory.DeletedEntry, records []inventory.BlobPurge) sweepFixture {
	t.Helper()

	f := sweepFixture{
		sweeper:   nil,
		expired:   &memExpired{entries: entries, calls: 0},
		lifecycle: &sweepLifecycle{mu: sync.Mutex{}, errs: map[string]error{}, entries: nil},
		records:   &memRecords{records: records, finished: nil, finishErr: nil},
		blobs:     &memBlobs{errs: map[string]error{}, deleted: nil},
		log:       &bytes.Buffer{},
	}

	sweeper, err := inventory.NewSweeper(inventory.SweeperConfig{
		Expired:    f.expired,
		BlobPurges: f.records,
		Lifecycles: map[string]inventory.Lifecycle{"pulumi": f.lifecycle},
		Blobs:      map[string]inventory.PurgedBlobs{"pulumi": f.blobs},
		Logger:     slog.New(slog.NewTextHandler(f.log, nil)),
		Now:        sweepNow,
	})
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}

	f.sweeper = sweeper

	return f
}

// sweepEntry is the audit entry that a sweep starts from.
func sweepEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "@server", IDPSubject: "", TokenID: "", Action: "unit:purge",
		Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
	}
}

// expiredUnits returns n deleted units of acme whose retention ended one
// hour before sweepNow, and one unit whose retention ends one hour after it.
func expiredUnits(n int) []inventory.DeletedEntry {
	out := make([]inventory.DeletedEntry, 0, n+1)

	for i := range n {
		out = append(out, inventory.DeletedEntry{
			DeletedAt:  sweepNow().Add(-31 * 24 * time.Hour),
			PurgeAfter: sweepNow().Add(-time.Hour),
			Namespace:  "acme",
			Tool:       "pulumi",
			UnitID:     fmt.Sprintf("stk-%03d", i),
			Name:       fmt.Sprintf("web/old-%03d", i),
		})
	}

	return append(out, inventory.DeletedEntry{
		DeletedAt:  sweepNow().Add(-29 * 24 * time.Hour),
		PurgeAfter: sweepNow().Add(time.Hour),
		Namespace:  "acme",
		Tool:       "pulumi",
		UnitID:     "stk-kept",
		Name:       "web/kept",
	})
}

// TestSweepPurgesExpiredUnitsOnly proves that a sweep purges each unit whose
// retention ended, page by page, and leaves a unit whose retention runs.
// Each purge carries the audit entry of the sweep with the namespace, the
// reference and the stable ID of its unit.
func TestSweepPurgesExpiredUnitsOnly(t *testing.T) {
	t.Parallel()

	n := 2*inventory.SweepPageSize + 7
	f := newSweepFixture(t, expiredUnits(n), nil)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(res.Purged) != n || res.Failed != 0 || res.Skipped != 0 {
		t.Fatalf(
			"sweep = %d purged, %d failed, %d skipped, want %d purged",
			len(res.Purged),
			res.Failed,
			res.Skipped,
			n,
		)
	}

	if f.expired.calls != 3 {
		t.Fatalf("store calls = %d, want 3 pages", f.expired.calls)
	}

	for _, e := range f.lifecycle.entries {
		if e.ObjectID == "stk-kept" {
			t.Fatal("sweep purged a unit whose retention still runs")
		}
	}

	checkFirstPurgeEntry(t, f.lifecycle.entries[0])
}

// checkFirstPurgeEntry checks the audit entry of the purge of stk-000.
func checkFirstPurgeEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Namespace != "acme" || e.Object != "pulumi:acme/web/old-000" || e.ObjectID != "stk-000" ||
		e.Subject != "@server" || e.Action != "unit:purge" {
		t.Fatalf("first purge entry = %+v, want acme, pulumi:acme/web/old-000, stk-000, @server, unit:purge", e)
	}
}

// TestSweepSkipsAndCountsFailures proves that a unit that a restore took
// first or whose namespace has its home elsewhere counts as skipped without a
// warning, that any other failure counts as failed with a warning that names
// the unit and the error kind but not the error text, and that the sweep
// goes on after a failure.
func TestSweepSkipsAndCountsFailures(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t, expiredUnits(4), nil)
	f.lifecycle.errs["stk-000"] = fmt.Errorf("purge: %w", inventory.ErrNotFound)
	f.lifecycle.errs["stk-001"] = fmt.Errorf("fence: %w", namespace.ErrNotHome)
	f.lifecycle.errs["stk-002"] = fmt.Errorf("purge: %w", errDriver)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(res.Purged) != 1 || res.Skipped != 2 || res.Failed != 1 {
		t.Fatalf("sweep = %d purged, %d skipped, %d failed, want 1, 2 and 1",
			len(res.Purged), res.Skipped, res.Failed)
	}

	logged := f.log.String()
	if !strings.Contains(logged, "pulumi:acme/web/old-002") || !strings.Contains(logged, "error_kind=store") {
		t.Fatalf("log = %q, want the reference of the failed unit and its error kind", logged)
	}

	if strings.Contains(logged, "10.0.0.7") || strings.Contains(logged, "old-000") ||
		strings.Contains(logged, "old-001") {
		t.Fatalf("log = %q, want no driver text and no warning for a skipped unit", logged)
	}
}

// TestSweepFinishesOnlyOldRecords proves the grace rule of the blob purge
// records: a record younger than BlobPurgeGrace stays and its blobs are not
// deleted again, an older record whose blobs are gone is finished, and an
// older record whose delete fails stays pending.
func TestSweepFinishesOnlyOldRecords(t *testing.T) {
	t.Parallel()

	old := sweepNow().Add(-inventory.BlobPurgeGrace - time.Minute)
	records := []inventory.BlobPurge{
		{PurgedAt: old, Namespace: "acme", Tool: "pulumi", UnitID: "stk-gone"},
		{PurgedAt: old, Namespace: "acme", Tool: "pulumi", UnitID: "stk-stuck"},
		{PurgedAt: sweepNow().Add(-time.Hour), Namespace: "acme", Tool: "pulumi", UnitID: "stk-young"},
	}
	f := newSweepFixture(t, nil, records)
	f.blobs.errs["stk-stuck"] = fmt.Errorf("delete: %w", errDriver)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if res.FinishedBlobPurges != 1 || res.PendingBlobPurges != 2 {
		t.Fatalf("sweep = %d finished, %d pending, want 1 and 2", res.FinishedBlobPurges, res.PendingBlobPurges)
	}

	if len(f.records.finished) != 1 || f.records.finished[0] != "pulumi/stk-gone" {
		t.Fatalf("finished = %v, want pulumi/stk-gone", f.records.finished)
	}

	for _, id := range f.blobs.deleted {
		if id == "stk-young" {
			t.Fatal("sweep deleted the blobs of a record inside its grace period")
		}
	}

	if strings.Contains(f.log.String(), "10.0.0.7") {
		t.Fatalf("log = %q, want no driver text", f.log.String())
	}
}

// TestSweepCountsAFailedFinish proves that a record whose finish fails stays
// pending and counts as a failure.
func TestSweepCountsAFailedFinish(t *testing.T) {
	t.Parallel()

	old := sweepNow().Add(-inventory.BlobPurgeGrace - time.Minute)
	f := newSweepFixture(
		t,
		nil,
		[]inventory.BlobPurge{{PurgedAt: old, Namespace: "acme", Tool: "pulumi", UnitID: "stk-1"}},
	)
	f.records.finishErr = fmt.Errorf("finish: %w", namespace.ErrUnavailable)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if res.FinishedBlobPurges != 0 || res.PendingBlobPurges != 1 || res.Failed != 1 {
		t.Fatalf("sweep = %+v, want one pending record and one failure", res)
	}

	if !strings.Contains(f.log.String(), "error_kind=unavailable") {
		t.Fatalf("log = %q, want the error kind unavailable", f.log.String())
	}
}

// TestSweepUnknownTool proves that a unit or a record of a tool without a
// port counts as failed or pending, and that the sweep goes on.
func TestSweepUnknownTool(t *testing.T) {
	t.Parallel()

	units := expiredUnits(1)
	units[0].Tool = "other"
	old := sweepNow().Add(-inventory.BlobPurgeGrace - time.Minute)
	f := newSweepFixture(
		t,
		units,
		[]inventory.BlobPurge{{PurgedAt: old, Namespace: "acme", Tool: "other", UnitID: "x"}},
	)

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if res.Failed != 1 || res.PendingBlobPurges != 1 || len(res.Purged) != 0 {
		t.Fatalf("sweep = %+v, want one failure and one pending record", res)
	}
}

// TestNewSweeperRefusesMissingPorts proves that NewSweeper names each
// missing port with ErrMissingDependency.
func TestNewSweeperRefusesMissingPorts(t *testing.T) {
	t.Parallel()

	full := func() inventory.SweeperConfig {
		return inventory.SweeperConfig{
			Expired:    &memExpired{entries: nil, calls: 0},
			BlobPurges: &memRecords{records: nil, finished: nil, finishErr: nil},
			Lifecycles: map[string]inventory.Lifecycle{
				"pulumi": &sweepLifecycle{mu: sync.Mutex{}, errs: nil, entries: nil},
			},
			Blobs:  map[string]inventory.PurgedBlobs{"pulumi": &memBlobs{errs: nil, deleted: nil}},
			Logger: slog.New(slog.DiscardHandler),
			Now:    nil,
		}
	}

	cases := []struct {
		name string
		drop func(*inventory.SweeperConfig)
	}{
		{name: "expired", drop: func(c *inventory.SweeperConfig) { c.Expired = nil }},
		{name: "records", drop: func(c *inventory.SweeperConfig) { c.BlobPurges = nil }},
		{name: "lifecycles", drop: func(c *inventory.SweeperConfig) { c.Lifecycles = nil }},
		{name: "blobs", drop: func(c *inventory.SweeperConfig) { c.Blobs = nil }},
		{name: "logger", drop: func(c *inventory.SweeperConfig) { c.Logger = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := full()
			tc.drop(&cfg)

			if _, err := inventory.NewSweeper(cfg); !errors.Is(err, inventory.ErrMissingDependency) {
				t.Fatalf("new sweeper without %s: err = %v, want ErrMissingDependency", tc.name, err)
			}
		})
	}

	if _, err := inventory.NewSweeper(full()); err != nil {
		t.Fatalf("new sweeper with every port: %v", err)
	}
}

// TestPageExpired proves the reference order of ListExpiredDeleted: the end
// of retention, then the tool, then the unit ID, with the cursor and the
// limit.
func TestPageExpired(t *testing.T) {
	t.Parallel()

	at := sweepNow().Add(-time.Hour)
	entries := []inventory.DeletedEntry{
		{DeletedAt: at, PurgeAfter: at, Namespace: "acme", Tool: "pulumi", UnitID: "b", Name: "web/b"},
		{DeletedAt: at, PurgeAfter: at, Namespace: "acme", Tool: "opentofu", UnitID: "z", Name: "z"},
		{
			DeletedAt:  at,
			PurgeAfter: at.Add(-time.Minute),
			Namespace:  "beta",
			Tool:       "pulumi",
			UnitID:     "c",
			Name:       "web/c",
		},
		{DeletedAt: at, PurgeAfter: at, Namespace: "acme", Tool: "pulumi", UnitID: "a", Name: "web/a"},
		{
			DeletedAt:  at,
			PurgeAfter: sweepNow().Add(time.Second),
			Namespace:  "acme",
			Tool:       "pulumi",
			UnitID:     "later",
			Name:       "x",
		},
		{DeletedAt: at, PurgeAfter: sweepNow(), Namespace: "acme", Tool: "pulumi", UnitID: "now", Name: "y"},
	}

	ids := func(page []inventory.DeletedEntry) string {
		out := make([]string, 0, len(page))
		for _, e := range page {
			out = append(out, e.UnitID)
		}

		return strings.Join(out, ",")
	}

	if got := ids(inventory.PageExpired(entries, sweepNow(), nil, 10)); got != "c,z,a,b,now" {
		t.Fatalf("expired = %s, want c,z,a,b,now", got)
	}

	cursor := &inventory.ExpiredCursor{PurgeAfter: at, Tool: "pulumi", UnitID: "a"}
	if got := ids(inventory.PageExpired(entries, sweepNow(), cursor, 1)); got != "b" {
		t.Fatalf("expired after pulumi/a, limit 1 = %s, want b", got)
	}
}
