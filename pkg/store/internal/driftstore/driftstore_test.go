package driftstore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/driftstore"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

var errTx = errors.New("transaction failed")

// epoch is the start of the job of the fixtures.
func epoch() time.Time { return time.UnixMilli(1_800_000_000_000).UTC() }

// fakeTx is a driftstore.Tx over maps. It counts the writes. Its Put keeps
// the rule of the port: it does not replace a row that is not older.
type fakeTx struct {
	jobs    map[job.ID]jobstore.Row
	latest  map[graph.Unit]driftstore.Row
	puts    int
	jobErr  error
	readErr error
	putErr  error
}

func newFakeTx() *fakeTx {
	return &fakeTx{
		jobs: map[job.ID]jobstore.Row{}, latest: map[graph.Unit]driftstore.Row{}, puts: 0, jobErr: nil,
		readErr: nil, putErr: nil,
	}
}

func (f *fakeTx) Job(_ context.Context, id job.ID) (jobstore.Row, bool, error) {
	row, ok := f.jobs[id]

	return row, ok, f.jobErr
}

func (f *fakeTx) Latest(_ context.Context, u graph.Unit) (driftstore.Row, bool, error) {
	row, ok := f.latest[u]

	return row, ok, f.readErr
}

func (f *fakeTx) Put(_ context.Context, r driftstore.Row) error {
	if f.putErr != nil {
		return f.putErr
	}

	u := graph.Unit{Namespace: namespace.Name(r.Namespace), Tool: r.Tool, ID: r.UnitID}
	if old, ok := f.latest[u]; ok && old.CheckedAtMs >= r.CheckedAtMs {
		return fmt.Errorf("put: %w", drift.ErrStale)
	}

	f.puts++
	f.latest[u] = r

	return nil
}

func unit() graph.Unit {
	return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "wsp-1"}
}

// jobBody returns the body of a job of the unit, as the job store stores it.
func jobBody(tool, ns, unitID, purpose string) string {
	return fmt.Sprintf(`{"spec":{"unit":{"tool":%q,"namespace":%q,"name":"network","unitID":%q},`+
		`"operation":"refresh","purpose":%q,"executor":"runner","location":{},"steps":[],"pool":"","profile":"",`+
		`"edits":[],"delivery":""},"phases":[],"source":"schedule"}`, tool, ns, unitID, purpose)
}

// jobRow returns a stored job of the unit that ran from epoch to epoch plus
// ten minutes.
func jobRow(id job.ID, state job.State, body string) jobstore.Row {
	return jobstore.Row{
		ID: id, Namespace: "acme", NamespaceID: "", UnitID: "wsp-1", Seq: 1, State: state, Wait: "", Phase: 0,
		Changes: false, Operation: "", Key: "", Version: 1, Body: body, CreatedMs: epoch().UnixMilli(),
		UpdatedMs:      epoch().Add(10 * time.Minute).UnixMilli(),
		StateChangedMs: 0,
	}
}

func goodJob(id job.ID) jobstore.Row {
	return jobRow(id, job.StateSucceeded, jobBody("opentofu", "acme", "wsp-1", "drift-check"))
}

func result(id job.ID, minute int, addresses ...string) *drift.Result {
	r := &drift.Result{
		CheckedAt: epoch().Add(time.Duration(minute) * time.Minute), Unit: unit(), JobID: id, Resources: nil,
		Truncated: false,
	}

	for _, a := range addresses {
		r.Resources = append(r.Resources, drift.Resource{Address: a, Kind: drift.KindChanged})
	}

	return r
}

func TestRecordStoresAndReturnsTheTransition(t *testing.T) {
	t.Parallel()

	tx := newFakeTx()
	tx.jobs["job-1"] = goodJob("job-1")

	tr, err := driftstore.Record(t.Context(), tx, result("job-1", 1, "a.b"))
	if err != nil {
		t.Fatalf("Record = %v", err)
	}

	if tr.From != drift.StatusUnknown || tr.To != drift.StatusDrifted || tx.puts != 1 {
		t.Fatalf("transition = %+v with %d writes, want unknown to drifted and one write", tr, tx.puts)
	}

	// The stored row reads back as the result.
	row := tx.latest[unit()]

	got, err := driftstore.Latest(&row)
	if err != nil || got.JobID != "job-1" || got.Status != drift.StatusDrifted || got.ResourceCount() != 1 {
		t.Fatalf("Latest = %+v, %v; want the drifted result of job-1", got, err)
	}
}

// TestRecordRefuses sends each hostile result straight to Record, as if the
// route and the adapter had checked nothing. Each case must change nothing.
func TestRecordRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(tx *fakeTx)
		res   *drift.Result
		want  error
	}{
		{
			"invalid result",
			func(tx *fakeTx) { tx.jobs["job-1"] = goodJob("job-1") },
			result("job-1", 1, "a\nb"),
			drift.ErrInvalid,
		},
		{"no such job", func(*fakeTx) {}, result("job-1", 1, "a"), drift.ErrJobMismatch},
		{
			"job body is not JSON",
			func(tx *fakeTx) { tx.jobs["job-1"] = jobRow("job-1", job.StateSucceeded, "not json") },
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"job body names another unit than its row",
			func(tx *fakeTx) {
				tx.jobs["job-1"] = jobRow(
					"job-1",
					job.StateSucceeded,
					jobBody("opentofu", "acme", "wsp-2", "drift-check"),
				)
			},
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"job row of another unit",
			func(tx *fakeTx) {
				row := goodJob("job-1")
				row.UnitID = "wsp-2"
				row.Body = jobBody("opentofu", "acme", "wsp-2", "drift-check")
				tx.jobs["job-1"] = row
			},
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"job of another namespace",
			func(tx *fakeTx) {
				row := goodJob("job-1")
				row.Namespace = "other"
				row.Body = jobBody("opentofu", "other", "wsp-1", "drift-check")
				tx.jobs["job-1"] = row
			},
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"job of another tool",
			func(tx *fakeTx) {
				tx.jobs["job-1"] = jobRow(
					"job-1",
					job.StateSucceeded,
					jobBody("pulumi", "acme", "wsp-1", "drift-check"),
				)
			},
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"job with no purpose",
			func(tx *fakeTx) {
				tx.jobs["job-1"] = jobRow("job-1", job.StateSucceeded, jobBody("opentofu", "acme", "wsp-1", ""))
			},
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"job with an unknown purpose",
			func(tx *fakeTx) {
				tx.jobs["job-1"] = jobRow("job-1", job.StateSucceeded, jobBody("opentofu", "acme", "wsp-1", "drift"))
			},
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"job still running",
			func(tx *fakeTx) {
				tx.jobs["job-1"] = jobRow(
					"job-1",
					job.StateRunning,
					jobBody("opentofu", "acme", "wsp-1", "drift-check"),
				)
			},
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"job failed",
			func(tx *fakeTx) {
				tx.jobs["job-1"] = jobRow("job-1", job.StateFailed, jobBody("opentofu", "acme", "wsp-1", "drift-check"))
			},
			result("job-1", 1, "a"), drift.ErrJobMismatch,
		},
		{
			"time before the job",
			func(tx *fakeTx) { tx.jobs["job-1"] = goodJob("job-1") },
			result("job-1", -1, "a"),
			drift.ErrJobMismatch,
		},
		{
			"time after the job",
			func(tx *fakeTx) { tx.jobs["job-1"] = goodJob("job-1") },
			result("job-1", 11, "a"),
			drift.ErrJobMismatch,
		},
		{
			"equal to the stored time",
			func(tx *fakeTx) {
				tx.jobs["job-1"], tx.jobs["job-0"] = goodJob("job-1"), goodJob("job-0")
				mustRecord(tx, result("job-0", 5, "x"))
			},
			result("job-1", 5, "a"), drift.ErrStale,
		},
		{
			"older than the stored time",
			func(tx *fakeTx) {
				tx.jobs["job-1"], tx.jobs["job-0"] = goodJob("job-1"), goodJob("job-0")
				mustRecord(tx, result("job-0", 5, "x"))
			},
			result("job-1", 4, "a"), drift.ErrStale,
		},
		{
			"the job of the stored result again, later",
			func(tx *fakeTx) {
				tx.jobs["job-0"] = goodJob("job-0")
				mustRecord(tx, result("job-0", 5, "x"))
			},
			result("job-0", 6, "a"), drift.ErrStale,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tx := newFakeTx()
			tt.setup(tx)

			before := tx.puts

			_, err := driftstore.Record(t.Context(), tx, tt.res)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Record = %v, want %v", err, tt.want)
			}

			if tx.puts != before {
				t.Errorf("a refused Record wrote %d rows", tx.puts-before)
			}
		})
	}
}

// mustRecord stores r directly, without any check, as an earlier result.
func mustRecord(tx *fakeTx, r *drift.Result) {
	row, err := driftstore.Encode(r)
	if err != nil {
		panic(err)
	}

	if err := tx.Put(context.Background(), row); err != nil {
		panic(err)
	}

	tx.puts--
}

func TestRecordPassesTheErrorsOfTheTransaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(tx *fakeTx)
	}{
		{"job read", func(tx *fakeTx) { tx.jobErr = errTx }},
		{"result read", func(tx *fakeTx) { tx.readErr = errTx }},
		{"result write", func(tx *fakeTx) { tx.putErr = errTx }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tx := newFakeTx()
			tx.jobs["job-1"] = goodJob("job-1")
			tt.setup(tx)

			_, err := driftstore.Record(t.Context(), tx, result("job-1", 1, "a"))
			if !errors.Is(err, errTx) || errors.Is(err, drift.ErrJobMismatch) || errors.Is(err, drift.ErrStale) {
				t.Fatalf("Record = %v, want the error of the transaction and no domain error", err)
			}
		})
	}
}

func TestRecordReplacesADamagedRow(t *testing.T) {
	t.Parallel()

	tx := newFakeTx()
	tx.jobs["job-1"] = goodJob("job-1")
	tx.latest[unit()] = damagedRow(epoch())

	tr, err := driftstore.Record(t.Context(), tx, result("job-1", 1, "a"))
	if err != nil || tr.From != drift.StatusUnknown || tx.puts != 1 {
		t.Fatalf("Record = %+v, %v with %d writes; want the new result to replace the damaged row", tr, err, tx.puts)
	}
}

// TestRecordKeepsADamagedRowThatIsNotOlder proves that a damaged row with a
// time that is not older than the new result stays, and that Record returns
// drift.ErrStale for it.
func TestRecordKeepsADamagedRowThatIsNotOlder(t *testing.T) {
	t.Parallel()

	for name, at := range map[string]time.Time{
		"the same time": epoch().Add(time.Minute), "a later time": epoch().Add(time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tx := newFakeTx()
			tx.jobs["job-1"] = goodJob("job-1")
			tx.latest[unit()] = damagedRow(at)

			tr, err := driftstore.Record(t.Context(), tx, result("job-1", 1, "a"))
			if !errors.Is(err, drift.ErrStale) || tr.JobID != "" || tx.puts != 0 {
				t.Fatalf(
					"Record = %+v, %v with %d writes; want drift result is not newer than the stored result and no write",
					tr,
					err,
					tx.puts,
				)
			}

			if got := tx.latest[unit()]; got != damagedRow(at) {
				t.Fatalf("stored row = %+v, want the damaged row", got)
			}
		})
	}
}

// damagedRow returns a stored row of the unit at the time at that fails its
// checks: a drifted result with no resource.
func damagedRow(at time.Time) driftstore.Row {
	return driftstore.Row{
		Namespace: "acme", Tool: "opentofu", UnitID: "wsp-1", JobID: "job-9", Status: "drifted",
		CheckedAtMs: at.UnixMilli(), Truncated: false, ResourcesJSON: "[]",
	}
}

// TestLatestRefuses sends each hostile row straight to Latest.
func TestLatestRefuses(t *testing.T) {
	t.Parallel()

	good := func() driftstore.Row {
		return driftstore.Row{
			Namespace: "acme", Tool: "opentofu", UnitID: "wsp-1", JobID: "job-1", Status: "drifted",
			CheckedAtMs: epoch().UnixMilli(), Truncated: false, ResourcesJSON: `[{"address":"a.b","kind":"changed"}]`,
		}
	}

	tests := []struct {
		name   string
		change func(r *driftstore.Row)
	}{
		{"resources are not JSON", func(r *driftstore.Row) { r.ResourcesJSON = "{" }},
		{"resources are an object", func(r *driftstore.Row) { r.ResourcesJSON = `{}` }},
		{"resources have an unknown field", func(r *driftstore.Row) {
			r.ResourcesJSON = `[{"address":"a","kind":"changed","value":"secret"}]`
		}},
		{"resources have text after the document", func(r *driftstore.Row) { r.ResourcesJSON += " []" }},
		{"address has a control character", func(r *driftstore.Row) {
			r.ResourcesJSON = `[{"address":"a\nb","kind":"changed"}]`
		}},
		{"address has a bidirectional control", func(r *driftstore.Row) {
			r.ResourcesJSON = `[{"address":"a\u202eb","kind":"changed"}]`
		}},
		{"kind is unknown", func(r *driftstore.Row) { r.ResourcesJSON = `[{"address":"a","kind":"created"}]` }},
		{"address twice", func(r *driftstore.Row) {
			r.ResourcesJSON = `[{"address":"a","kind":"changed"},{"address":"a","kind":"deleted"}]`
		}},
		{"too many resources", func(r *driftstore.Row) {
			var b strings.Builder

			b.WriteString("[")

			for i := range drift.MaxResources + 1 {
				if i > 0 {
					b.WriteString(",")
				}

				fmt.Fprintf(&b, `{"address":"a%d","kind":"changed"}`, i)
			}

			b.WriteString("]")

			r.ResourcesJSON = b.String()
		}},
		{"status in-sync with a resource", func(r *driftstore.Row) { r.Status = "in-sync" }},
		{"status drifted with no resource", func(r *driftstore.Row) { r.ResourcesJSON = "[]" }},
		{"status unknown", func(r *driftstore.Row) { r.Status = "unknown" }},
		{"job ID has no prefix", func(r *driftstore.Row) { r.JobID = "x-1" }},
		{"time is zero", func(r *driftstore.Row) { r.CheckedAtMs = time.Time{}.UnixMilli() }},
		{"unit ID is empty", func(r *driftstore.Row) { r.UnitID = "" }},
		{"namespace is not valid", func(r *driftstore.Row) { r.Namespace = "A B" }},
		{"truncated with few resources", func(r *driftstore.Row) { r.Truncated = true }},
	}

	row := good()
	if _, err := driftstore.Latest(&row); err != nil {
		t.Fatalf("Latest of the good row = %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			row := good()
			tt.change(&row)

			if _, err := driftstore.Latest(&row); !errors.Is(err, driftstore.ErrCorrupt) {
				t.Fatalf("Latest = %v, want ErrCorrupt", err)
			}
		})
	}
}
