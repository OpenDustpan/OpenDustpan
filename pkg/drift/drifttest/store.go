package drifttest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func storeCases() []suiteCase {
	return []suiteCase{
		{"store: a drifted result is the latest result", testRecordDrifted},
		{"store: a clean result clears the drift", testRecordClears},
		{"store: a repeated status is no change", testRecordSameStatus},
		{"store: the kinds, the order and the truncated mark of a result come back", testRecordRoundTrip},
		{"store: a result that fails validation changes nothing", testRecordInvalid},
		{"store: an older or equal result is stale", testRecordStale},
		{"store: concurrent records never leave an older result", testRecordConcurrent},
		{"store: the same job twice is stale", testRecordSameJob},
		{"store: a job that is not a finished drift check of the unit is refused", testRecordJobMismatch},
		{"store: a result checked outside the run of its job is refused", testRecordOutsideJob},
		{"store: a stale fence or the fence of another namespace refuses each write", testStoreFenced},
		{"store: a delete removes the result and is idempotent", testStoreDelete},
		{"store: a unit with no result is not found", testGetNotFound},
		{"store: the result of one unit does not reach another unit", testStoreIsolation},
		{"store: the inventory projection follows the status", testProjection},
		{"store: one notification for a change into drifted, none for a repeat", testNotification},
	}
}

func testRecordDrifted(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")
	id := h.Jobs.DriftCheck(t, u)

	want := []string{"aws_instance.web", "aws_s3_bucket.logs"}

	tr, err := h.Store.Record(t.Context(), result(u, id, 1, want...), f)
	must(t, "record", err)

	if tr.From != drift.StatusUnknown || tr.To != drift.StatusDrifted || !tr.BecameDrifted() || tr.JobID != id {
		t.Errorf("transition = %+v, want unknown to drifted for job %s", tr, id)
	}

	got, err := h.Store.GetLatest(t.Context(), u)
	must(t, "get", err)

	if got.Status != drift.StatusDrifted || got.JobID != id || !got.CheckedAt.Equal(at(1)) ||
		got.ResourceCount() != len(want) {
		t.Errorf("latest = %+v, want drifted from job %s at minute 1 with %d resources", got, id, len(want))
	}

	addresses := make([]string, 0, len(got.Resources))
	for _, r := range got.Resources {
		addresses = append(addresses, r.Address)
	}

	slices.Sort(addresses)

	if !slices.Equal(addresses, want) {
		t.Errorf("addresses = %v, want %v", addresses, want)
	}
}

func testRecordRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")

	// The longest result: the limit of resources, cut by the runner, with both
	// kinds and an address that holds the characters of a real one.
	r := drift.Result{CheckedAt: at(1), Unit: u, JobID: h.Jobs.DriftCheck(t, u), Resources: nil, Truncated: true}

	for i := range drift.MaxResources {
		kind := drift.KindChanged
		if i%2 == 1 {
			kind = drift.KindDeleted
		}

		r.Resources = append(r.Resources, drift.Resource{
			Address: fmt.Sprintf("module.net[\"eu-%d\"].aws_subnet.private[%d]", i, i), Kind: kind,
		})
	}

	_, err := h.Store.Record(t.Context(), r, f)
	must(t, "record", err)

	got, err := h.Store.GetLatest(t.Context(), u)
	must(t, "get", err)

	if !got.Truncated || got.Status != drift.StatusDrifted || got.ResourceCount() != drift.MaxResources {
		t.Fatalf("latest = truncated %t, status %q, %d resources; want the truncated result of %d",
			got.Truncated, got.Status, got.ResourceCount(), drift.MaxResources)
	}

	sorted := func(rs []drift.Resource) []drift.Resource {
		out := slices.Clone(rs)
		slices.SortFunc(out, func(a, b drift.Resource) int { return strings.Compare(a.Address, b.Address) })

		return out
	}

	if !slices.Equal(sorted(got.Resources), sorted(r.Resources)) {
		t.Error("the addresses or the kinds of the latest result differ from the recorded result")
	}

	// A newer result that is not truncated replaces the whole result.
	next := result(u, h.Jobs.DriftCheck(t, u), minute2, "only.one")

	_, err = h.Store.Record(t.Context(), next, f)
	must(t, "second record", err)

	got, err = h.Store.GetLatest(t.Context(), u)
	must(t, "second get", err)

	if got.Truncated || got.ResourceCount() != 1 || got.Resources[0].Address != "only.one" {
		t.Errorf("latest = %+v, want the second result alone", got)
	}
}

func testRecordClears(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")

	_, err := h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), 1, "a"), f)
	must(t, "first record", err)

	tr, err := h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), minute2), f)
	must(t, "clean record", err)

	if !tr.Cleared() || tr.From != drift.StatusDrifted || tr.To != drift.StatusInSync {
		t.Errorf("transition = %+v, want drifted to in-sync", tr)
	}

	got, err := h.Store.GetLatest(t.Context(), u)
	must(t, "get", err)

	if got.Status != drift.StatusInSync || got.ResourceCount() != 0 {
		t.Errorf("latest = %+v, want in-sync with no resource", got)
	}
}

func testRecordSameStatus(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")

	_, err := h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), 1, "a"), f)
	must(t, "first record", err)

	second := []string{"a", "b"}

	tr, err := h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), minute2, second...), f)
	must(t, "second record", err)

	if tr.Changed() || tr.BecameDrifted() {
		t.Errorf("transition = %+v, want no change", tr)
	}

	got, err := h.Store.GetLatest(t.Context(), u)
	must(t, "get", err)

	if got.ResourceCount() != len(second) || !got.CheckedAt.Equal(at(minute2)) {
		t.Errorf("latest = %+v, want the newer result with %d resources", got, len(second))
	}
}

func testRecordInvalid(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")
	id := h.Jobs.DriftCheck(t, u)

	// The store must refuse each hostile result by itself, as if the runner
	// route had not checked it (defense in depth).
	zero := result(u, id, 1, "a")
	zero.CheckedAt = time.Time{}

	hostile := map[string]drift.Result{
		"control character": result(u, id, 1, "a\nb"),
		"bidirectional":     result(u, id, 1, "a\u202Eb"),
		"empty address":     result(u, id, 1, ""),
		"long address":      result(u, id, 1, strings.Repeat("a", drift.MaxAddressBytes+1)),
		"address twice":     result(u, id, 1, "a", "a"),
		"zero time":         zero,
	}

	for name, r := range hostile {
		_, err := h.Store.Record(t.Context(), r, f)
		wantErr(t, name, err, drift.ErrInvalid)
	}

	_, err := h.Store.GetLatest(t.Context(), u)
	wantErr(t, "get after refused records", err, drift.ErrNotFound)
}

func testRecordStale(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")

	_, err := h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), minute5, "a"), f)
	must(t, "first record", err)

	for _, n := range []int{minute4, minute5} {
		_, err = h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), n), f)
		wantErr(t, "older or equal result", err, drift.ErrStale)
	}

	got, err := h.Store.GetLatest(t.Context(), u)
	must(t, "get", err)

	if got.Status != drift.StatusDrifted || !got.CheckedAt.Equal(at(minute5)) {
		t.Errorf("latest = %+v, want the first result unchanged", got)
	}
}

// testRecordConcurrent sends the results of minutes 1 to 9 of one unit at the
// same time, so that two writers can read the same stored row. Each record
// returns nil or ErrStale, the newest one returns nil, and the stored result
// is the newest one, whatever the order of the writers.
func testRecordConcurrent(t *testing.T, h Harness) {
	t.Helper()

	const first, last = 1, 9

	f := h.Namespace(t, "acme")

	for round := range 3 {
		u := unit("acme", fmt.Sprintf("wsp-%d", round))
		ids := make(map[int]job.ID, last)

		// Jobs are made before the writers start: the factory may stop the test.
		for n := first; n <= last; n++ {
			ids[n] = h.Jobs.DriftCheck(t, u)
		}

		errs := make(map[int]error, last)

		var (
			wg sync.WaitGroup
			mu sync.Mutex
		)

		for n := first; n <= last; n++ {
			wg.Go(func() {
				_, err := h.Store.Record(t.Context(), result(u, ids[n], n, "a"), f)

				mu.Lock()
				errs[n] = err
				mu.Unlock()
			})
		}

		wg.Wait()

		for n, err := range errs {
			if err != nil && !errors.Is(err, drift.ErrStale) {
				t.Errorf("round %d: record of minute %d = %v, want nil or ErrStale", round, n, err)
			}
		}

		if errs[last] != nil {
			t.Errorf("round %d: record of the newest minute = %v, want nil", round, errs[last])
		}

		got, err := h.Store.GetLatest(t.Context(), u)
		must(t, "get", err)

		if !got.CheckedAt.Equal(at(last)) || got.JobID != ids[last] {
			t.Errorf("round %d: latest = %+v, want the result of minute %d", round, got, last)
		}
	}
}

func testRecordSameJob(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")
	id := h.Jobs.DriftCheck(t, u)

	_, err := h.Store.Record(t.Context(), result(u, id, 1, "a"), f)
	must(t, "first record", err)

	_, err = h.Store.Record(t.Context(), result(u, id, minute2, "a"), f)
	wantErr(t, "second record of the same job", err, drift.ErrStale)
}

func testRecordJobMismatch(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")
	other := unit("acme", "wsp-2")

	jobs := map[string]job.ID{
		"a drift check of another unit":   h.Jobs.DriftCheck(t, other),
		"a job that is not a drift check": h.Jobs.Plain(t, u),
		"a drift check that did not end":  h.Jobs.Running(t, u),
		"a failed drift check":            h.Jobs.Failed(t, u),
		"a canceled drift check":          h.Jobs.Canceled(t, u),
		"a job that does not exist":       "job-does-not-exist",
	}

	for name, id := range jobs {
		_, err := h.Store.Record(t.Context(), result(u, id, 1, "a"), f)
		wantErr(t, name, err, drift.ErrJobMismatch)
	}

	_, err := h.Store.GetLatest(t.Context(), u)
	wantErr(t, "get after refused records", err, drift.ErrNotFound)
}

func testRecordOutsideJob(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")
	id := h.Jobs.DriftCheck(t, u)

	// A runner sets the time of its own result. A time after the job ended
	// would make every later result stale, so the store refuses it. The far
	// future is the hostile case.
	late := result(u, id, minuteLate, "a")
	farFuture := result(u, id, 1, "a")
	farFuture.CheckedAt = time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
	early := result(u, id, -1, "a")

	for name, r := range map[string]drift.Result{
		"a result checked after its job ended": late,
		"a result checked in the far future":   farFuture,
		"a result checked before its job":      early,
	} {
		_, err := h.Store.Record(t.Context(), r, f)
		wantErr(t, name, err, drift.ErrJobMismatch)
	}

	_, err := h.Store.GetLatest(t.Context(), u)
	wantErr(t, "get after refused records", err, drift.ErrNotFound)

	// The job is still usable with a time inside its run.
	_, err = h.Store.Record(t.Context(), result(u, id, 1, "a"), f)
	must(t, "record inside the run", err)
}

func testStoreFenced(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	other := h.Namespace(t, "other")
	u := unit("acme", "wsp-1")
	id := h.Jobs.DriftCheck(t, u)

	for name, bad := range map[string]namespace.Fence{"stale fence": stale(f), "fence of another namespace": other} {
		_, err := h.Store.Record(t.Context(), result(u, id, 1, "a"), bad)
		wantErr(t, "record with a "+name, err, namespace.ErrFenced)

		wantErr(t, "delete with a "+name, h.Store.DeleteLatest(t.Context(), u, bad), namespace.ErrFenced)
	}

	_, err := h.Store.GetLatest(t.Context(), u)
	wantErr(t, "get after fenced writes", err, drift.ErrNotFound)
}

func testStoreDelete(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")

	must(t, "delete with no result", h.Store.DeleteLatest(t.Context(), u, f))

	_, err := h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), 1, "a"), f)
	must(t, "record", err)
	must(t, "delete", h.Store.DeleteLatest(t.Context(), u, f))

	_, err = h.Store.GetLatest(t.Context(), u)
	wantErr(t, "get after delete", err, drift.ErrNotFound)

	if h.Projection != nil {
		if got := h.Projection(t, u); got != drift.StatusUnknown {
			t.Errorf("projection after delete = %q, want unknown", got)
		}
	}

	// A new check after the delete is a first result again.
	tr, err := h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), 1, "a"), f)
	must(t, "record after delete", err)

	if tr.From != drift.StatusUnknown {
		t.Errorf("transition after delete = %+v, want from unknown", tr)
	}
}

func testGetNotFound(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")

	_, err := h.Store.GetLatest(t.Context(), unit("acme", "wsp-1"))
	wantErr(t, "get with no result", err, drift.ErrNotFound)
}

func testStoreIsolation(t *testing.T, h Harness) {
	t.Helper()

	f := h.Namespace(t, "acme")
	a := unit("acme", "wsp-1")
	b := unit("acme", "wsp-2")

	_, err := h.Store.Record(t.Context(), result(a, h.Jobs.DriftCheck(t, a), 1, "x"), f)
	must(t, "record", err)

	_, err = h.Store.GetLatest(t.Context(), b)
	wantErr(t, "get of another unit", err, drift.ErrNotFound)
}

func testProjection(t *testing.T, h Harness) {
	t.Helper()

	if h.Projection == nil {
		t.Skip("the adapter gives no projection hook")
	}

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")

	if got := h.Projection(t, u); got != drift.StatusUnknown {
		t.Fatalf("projection before any check = %q, want unknown", got)
	}

	_, err := h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), 1, "a"), f)
	must(t, "record drifted", err)

	if got := h.Projection(t, u); got != drift.StatusDrifted {
		t.Errorf("projection = %q, want drifted", got)
	}

	_, err = h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), minute2), f)
	must(t, "record clean", err)

	if got := h.Projection(t, u); got != drift.StatusInSync {
		t.Errorf("projection = %q, want in-sync", got)
	}

	// A refused record leaves the projection as it was.
	_, err = h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), 1, "a"), f)
	wantErr(t, "stale record", err, drift.ErrStale)

	if got := h.Projection(t, u); got != drift.StatusInSync {
		t.Errorf("projection after a refused record = %q, want in-sync", got)
	}
}

func testNotification(t *testing.T, h Harness) {
	t.Helper()

	if h.Notified == nil {
		t.Skip("the adapter gives no notification hook")
	}

	f := h.Namespace(t, "acme")
	u := unit("acme", "wsp-1")

	first := h.Jobs.DriftCheck(t, u)

	_, err := h.Store.Record(t.Context(), result(u, first, 1, "a"), f)
	must(t, "first drifted record", err)

	want := 1

	got := h.Notified(t, "acme")
	if len(got) != want {
		t.Fatalf("notifications after a change into drifted = %v, want %d", got, want)
	}

	// The dedupe key holds the unit ID and the job ID.
	if !strings.Contains(got[0], u.ID) || !strings.Contains(got[0], string(first)) {
		t.Errorf("dedupe key = %q, want the unit ID %q and the job ID %q", got[0], u.ID, first)
	}

	_, err = h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), minute2, "a", "b"), f)
	must(t, "second drifted record", err)

	if got := h.Notified(t, "acme"); len(got) != want {
		t.Errorf("notifications after a repeat = %v, want still %d", got, want)
	}

	_, err = h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), minute3), f)
	must(t, "clean record", err)

	want++

	if got := h.Notified(t, "acme"); len(got) != want {
		t.Errorf("notifications after the drift cleared = %v, want %d", got, want)
	}

	// A refused record sends nothing.
	_, err = h.Store.Record(t.Context(), result(u, h.Jobs.DriftCheck(t, u), 1, "a"), f)
	wantErr(t, "stale record", err, drift.ErrStale)

	if got := h.Notified(t, "acme"); len(got) != want {
		t.Errorf("notifications after a refused record = %v, want still %d", got, want)
	}
}
