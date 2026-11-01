package job_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/blob/blobfault"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Keys of the cases: job-1 in the namespace sweepNS.
const (
	sweepNS    = namespace.ID("ns-sweep")
	sweepJob   = job.ID("job-1")
	chunkKey   = "job/job-1/log/jlv-1/00000003-jla-abc"
	planPart   = "job/job-1/plan/pln-1/plan-display"
	sweepAhead = blob.UnnamedGracePeriod + time.Minute
)

func TestParseArtifactKey(t *testing.T) {
	t.Parallel()

	good := []struct {
		rel  string
		want job.ArtifactKey
	}{
		{chunkKey, job.ArtifactKey{Plan: "", Kind: "", Version: "jlv-1", Seq: 3}},
		{planPart, job.ArtifactKey{Plan: "pln-1", Kind: job.OutputPlanDisplay, Version: "", Seq: 0}},
		{
			"job/job-1/plan/pln-1/plan-apply",
			job.ArtifactKey{Plan: "pln-1", Kind: job.OutputPlanApply, Version: "", Seq: 0},
		},
	}

	for _, tc := range good {
		got, err := job.ParseArtifactKey(sweepJob, tc.rel)
		if err != nil || got != tc.want {
			t.Errorf("ParseArtifactKey(%q) = %+v, %v; want %+v", tc.rel, got, err, tc.want)
		}
	}

	hostile := []string{
		"",
		"job/job-2/log/jlv-1/00000003-jla-abc",
		"job/job-1/log/jlv-1/3-jla-abc",
		"job/job-1/log/jlv-1/00000003",
		"job/job-1/log/jlv-1/00000003-other",
		"job/job-1/log/../00000003-jla-abc",
		"job/job-1/log/jlv-1/00000003-jla-abc/x",
		"job/job-1/plan/pln-1/plan-other",
		"job/job-1/plan/other-1/plan-display",
		"job/job-1/plan/pln-1/../plan-display",
		"job/job-1/other/pln-1/plan-display",
		"namespaces/ns-sweep/job/job-1/plan/pln-1/plan-display",
		"job/job-1/plan/PLN-1/plan-display",
		"job/job-1/log/jlv-1/-0000003-jla-abc",
		"job/job-1/log/jlv-1/99999999-jla-abc",
		"job/job-1/log/jlv-1\n/00000003-jla-abc",
	}

	for _, rel := range hostile {
		if got, err := job.ParseArtifactKey(sweepJob, rel); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("ParseArtifactKey(%q) = %+v, %v; want ErrInvalid", rel, got, err)
		}
	}
}

// TestDeleteJobBlobRefusesOtherKeys sends hostile keys to the delete, as if
// the sweep had failed to parse them: the delete refuses each one itself and
// deletes nothing.
func TestDeleteJobBlobRefusesOtherKeys(t *testing.T) {
	t.Parallel()

	b, store, _ := newArtifactBlobs(t)

	victim := blob.Key("namespaces/" + sweepNS.String() + "/job/job-2/plan/pln-1/plan-display")
	if err := store.Put(t.Context(), victim, strings.NewReader("x")); err != nil {
		t.Fatalf("Put = %v", err)
	}

	for _, rel := range []string{
		"job/job-2/plan/pln-1/plan-display", "../job/job-2/plan/pln-1/plan-display", "job/job-1/other", "",
		"job/job-1/plan/pln-1/../../job-2/plan/pln-1/plan-display",
	} {
		if err := b.DeleteJobBlob(t.Context(), sweepNS, sweepJob, rel); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("DeleteJobBlob(%q) = %v, want ErrInvalid", rel, err)
		}
	}

	if got := store.Calls("Delete"); got != 0 {
		t.Fatalf("store got %d deletes, want 0", got)
	}

	if _, err := store.Get(t.Context(), victim); err != nil {
		t.Fatalf("the blob of another job is gone: %v", err)
	}
}

// strayStore answers each list with one key outside the directory that the
// list names, as a broken store could.
type strayStore struct {
	blob.Store
}

func (s strayStore) ListPage(context.Context, blob.PageRequest) (blob.Page, error) {
	return blob.Page{Objects: []blob.Object{{
		ModifiedAt: time.Now().Add(-time.Hour * 24), Key: blob.Key("namespaces/" + sweepNS.String() + "/job/job-2/x"),
	}}, Next: ""}, nil
}

// TestWalkJobRefusesAStrayKey shows that a key outside the directory of the
// job stops the walk with ErrOutOfScope, before the sweep sees it.
func TestWalkJobRefusesAStrayKey(t *testing.T) {
	t.Parallel()

	_, store, refusals := newArtifactBlobs(t)

	b, err := job.NewArtifactBlobs(strayStore{Store: store}, refusals)
	if err != nil {
		t.Fatalf("NewArtifactBlobs = %v", err)
	}

	seen := 0

	err = b.WalkJob(t.Context(), sweepNS, sweepJob, func(page []job.ArtifactBlob) error {
		seen += len(page)

		return nil
	})
	if !errors.Is(err, blob.ErrOutOfScope) || seen != 0 {
		t.Fatalf("WalkJob = %v after %d blobs, want ErrOutOfScope and none", err, seen)
	}

	if refusals.n.Load() != 1 {
		t.Fatalf("refusals = %d, want 1", refusals.n.Load())
	}

	for _, id := range []job.ID{"", "job-1/../job-2", "other-1"} {
		if err := b.WalkJob(t.Context(), sweepNS, id, func([]job.ArtifactBlob) error { return nil }); !errors.Is(err,
			job.ErrInvalid) {
			t.Errorf("WalkJob(%q) = %v, want ErrInvalid", id, err)
		}
	}
}

// errNamesRead is the failure of a read of the names in the cases.
var errNamesRead = errors.New("read of the names failed")

// sweepNames is an ArtifactKeyStore with fixed names or a fixed error.
type sweepNames struct {
	names job.ArtifactNames
	err   error
}

func (s sweepNames) ArtifactNames(context.Context, namespace.Name, job.ID) (job.ArtifactNames, error) {
	return s.names, s.err
}

// sweepNamespaces reads every namespace as sweepNS.
type sweepNamespaces struct{}

func (sweepNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		ID: sweepNS, CreatedAt: time.Time{}, Name: name, PrimaryInstallation: "", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// listedJobs is a job store that answers List with one job and records each
// filter it gets.
type listedJobs struct {
	job.Store

	j       job.Job
	mu      sync.Mutex
	filters []job.Filter
}

func (l *listedJobs) List(_ context.Context, f job.Filter) ([]job.Job, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.filters = append(l.filters, f)

	return []job.Job{l.j}, nil
}

// noWatch answers that nothing runs.
type noWatch struct{}

func (noWatch) Running(context.Context) (blobguard.Work, bool, error) {
	return blobguard.Work{Kind: "", ID: ""}, false, nil
}

// sweepSetup is the setup of one sweep case: the names that the rows give,
// how far the clock of the database runs ahead of the clock of the test, how
// far the clock of the instance runs ahead of the database, and a wrapper of
// the blob store.
type sweepSetup struct {
	names    job.ArtifactKeyStore
	ahead    time.Duration
	instance time.Duration
	wrap     func(blob.Store) blob.Store
}

// sweepRig is one sweep case: the sweeper, the blob store, the job store and
// the audit entries.
type sweepRig struct {
	s     *job.ArtifactSweeper
	store *blobfault.Store
	jobs  *listedJobs
	audit *recordedAudit
}

// sweepCase builds a sweeper over a blob store that holds the chunk and the
// plan part, with names and the clocks of the database and of the instance
// ahead by ahead.
func sweepCase(t *testing.T, names job.ArtifactKeyStore, ahead time.Duration) (*job.ArtifactSweeper,
	*blobfault.Store, *listedJobs,
) {
	t.Helper()

	r := newSweepRig(t, sweepSetup{names: names, ahead: ahead, instance: 0, wrap: nil})

	return r.s, r.store, r.jobs
}

// newSweepRig builds the sweep case of setup.
func newSweepRig(t *testing.T, setup sweepSetup) *sweepRig {
	t.Helper()

	_, store, refusals := newArtifactBlobs(t)

	var inner blob.Store = store
	if setup.wrap != nil {
		inner = setup.wrap(store)
	}

	b, err := job.NewArtifactBlobs(inner, refusals)
	if err != nil {
		t.Fatalf("NewArtifactBlobs = %v", err)
	}

	for _, rel := range []string{chunkKey, planPart} {
		if putErr := store.Put(t.Context(), blob.Key(sweepPrefix()+rel), strings.NewReader("x")); putErr != nil {
			t.Fatalf("Put = %v", putErr)
		}
	}

	r := &sweepRig{
		s: nil, store: store, jobs: &listedJobs{Store: nil, j: sweepJobOf(t), mu: sync.Mutex{}, filters: nil},
		audit: &recordedAudit{mu: sync.Mutex{}, entries: nil},
	}

	r.s, err = job.NewArtifactSweeper(job.ArtifactSweeperConfig{
		Jobs: r.jobs, Names: setup.names, Blobs: b, Namespaces: sweepNamespaces{}, Guard: noWatch{},
		Clock: aheadClock{ahead: setup.ahead}, Audit: r.audit, Logger: nil,
		Now: func() time.Time { return time.Now().Add(setup.ahead + setup.instance) },
	})
	if err != nil {
		t.Fatalf("NewArtifactSweeper = %v", err)
	}

	return r
}

// aheadClock is a database clock that runs ahead of the clock of the test.
type aheadClock struct{ ahead time.Duration }

func (c aheadClock) DatabaseNow(context.Context) (time.Time, error) {
	return time.Now().Add(c.ahead), nil
}

// recordedAudit is an audit.ServerStore that keeps each entry.
type recordedAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordedAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, fmt.Errorf("append: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}

// only returns the one entry, and fails t when there is not exactly one.
func (a *recordedAudit) only(t *testing.T) audit.Entry {
	t.Helper()

	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(a.entries))
	}

	return a.entries[0]
}

// sweepPrefix is the key prefix of sweepNS.
func sweepPrefix() string { return "namespaces/" + sweepNS.String() + "/" }

// sweepJobOf returns job-1 on a unit of the namespace acme.
func sweepJobOf(t *testing.T) job.Job {
	t.Helper()

	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	var j job.Job

	j.ID = sweepJob
	j.Spec.Unit = unit

	return j
}

// TestSweepFailsClosed shows that a failed read of the names, and a job that
// the store cannot find, delete nothing.
func TestSweepFailsClosed(t *testing.T) {
	t.Parallel()

	for _, err := range []error{errNamesRead, job.ErrNotFound} {
		s, store, jobs := sweepCase(t, sweepNames{names: job.ArtifactNames{Plans: nil, ChunkKeys: nil}, err: err},
			sweepAhead)

		if _, sweepErr := s.SweepJob(t.Context(), &jobs.j); !errors.Is(sweepErr, err) {
			t.Errorf("SweepJob with %v = %v, want it", err, sweepErr)
		}

		requireStored(t, store, chunkKey, planPart)
	}
}

// TestSweepKeepsNamedAndYoungBlobs shows the rule of the sweep on one job: a
// young blob stays, a named blob stays, and an old blob that no row names
// goes.
func TestSweepKeepsNamedAndYoungBlobs(t *testing.T) {
	t.Parallel()

	named := sweepNames{names: job.ArtifactNames{Plans: []string{"pln-1"}, ChunkKeys: nil}, err: nil}

	s, store, jobs := sweepCase(t, named, 0)
	if res, err := s.SweepJob(t.Context(), &jobs.j); err != nil || res.Deleted != 0 {
		t.Fatalf("young sweep = %+v, %v; want nothing deleted", res, err)
	}

	requireStored(t, store, chunkKey, planPart)

	s, store, jobs = sweepCase(t, named, sweepAhead)
	if res, err := s.SweepJob(t.Context(), &jobs.j); err != nil || res.Deleted != 1 {
		t.Fatalf("old sweep = %+v, %v; want 1 deleted", res, err)
	}

	requireStored(t, store, planPart)

	if _, err := store.Get(t.Context(), blob.Key(sweepPrefix()+chunkKey)); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get of the unnamed chunk = %v, want ErrNotFound", err)
	}
}

// TestSweepPasses shows the two passes of a run: the jobs that have not ended,
// then the jobs whose state changed in the window.
func TestSweepPasses(t *testing.T) {
	t.Parallel()

	s, _, jobs := sweepCase(t, sweepNames{names: job.ArtifactNames{Plans: nil, ChunkKeys: nil}, err: nil}, 0)

	if _, err := s.Sweep(t.Context()); err != nil {
		t.Fatalf("Sweep = %v", err)
	}

	if len(jobs.filters) != 2 {
		t.Fatalf("Sweep made %d lists, want 2", len(jobs.filters))
	}

	live := jobs.filters[0]
	for _, st := range live.States {
		if st.Terminal() {
			t.Errorf("the first pass lists the end state %s", st)
		}
	}

	if len(live.States) == 0 || !live.StateChangedAfter.IsZero() {
		t.Errorf("first pass = %+v, want the live states and no time", live)
	}

	window := jobs.filters[1]
	if len(window.States) != 0 || window.StateChangedAfter.IsZero() ||
		time.Since(window.StateChangedAfter) < job.ArtifactSweepWindow-time.Minute {
		t.Errorf("second pass = %+v, want every state changed in the window", window)
	}
}

func TestNewArtifactSweeperNeedsItsPorts(t *testing.T) {
	t.Parallel()

	b, _, _ := newArtifactBlobs(t)
	full := job.ArtifactSweeperConfig{
		Jobs:  &listedJobs{Store: nil, j: sweepJobOf(t), mu: sync.Mutex{}, filters: nil},
		Names: sweepNames{names: job.ArtifactNames{Plans: nil, ChunkKeys: nil}, err: nil},
		Blobs: b, Namespaces: sweepNamespaces{}, Guard: noWatch{}, Clock: aheadClock{ahead: 0},
		Audit: &recordedAudit{mu: sync.Mutex{}, entries: nil}, Logger: nil, Now: nil,
	}

	for name, edit := range map[string]func(c *job.ArtifactSweeperConfig){
		"jobs":       func(c *job.ArtifactSweeperConfig) { c.Jobs = nil },
		"names":      func(c *job.ArtifactSweeperConfig) { c.Names = nil },
		"blobs":      func(c *job.ArtifactSweeperConfig) { c.Blobs = nil },
		"namespaces": func(c *job.ArtifactSweeperConfig) { c.Namespaces = nil },
		"guard":      func(c *job.ArtifactSweeperConfig) { c.Guard = nil },
		"clock":      func(c *job.ArtifactSweeperConfig) { c.Clock = nil },
		"audit":      func(c *job.ArtifactSweeperConfig) { c.Audit = nil },
	} {
		cfg := full
		edit(&cfg)

		if _, err := job.NewArtifactSweeper(cfg); !errors.Is(err, job.ErrInvalid) {
			t.Errorf("NewArtifactSweeper with no %s = %v, want ErrInvalid", name, err)
		}
	}
}

// requireStored checks that each key, relative to sweepNS, is stored.
func requireStored(t *testing.T, store blob.Store, rels ...string) {
	t.Helper()

	for _, rel := range rels {
		if _, err := store.Get(t.Context(), blob.Key(sweepPrefix()+rel)); err != nil {
			t.Errorf("Get(%q) = %v, want it stored", rel, err)
		}
	}
}

// TestSweepRecordsItsRun shows that a run lists the keys that it deleted in
// its result, and appends one audit entry with its counts and its reason.
func TestSweepRecordsItsRun(t *testing.T) {
	t.Parallel()

	named := sweepNames{names: job.ArtifactNames{Plans: []string{"pln-1"}, ChunkKeys: nil}, err: nil}
	r := newSweepRig(t, sweepSetup{names: named, ahead: sweepAhead, instance: 0, wrap: nil})

	res, err := r.s.Sweep(t.Context())
	if err != nil {
		t.Fatalf("Sweep = %v", err)
	}

	// The fake job store lists the job in each of the two passes.
	want := []string{sweepPrefix() + chunkKey}
	if res.Deleted != 1 || !slices.Equal(res.DeletedKeys, want) || res.Truncated || res.Skipped != "" {
		t.Fatalf("Sweep = %+v, want the chunk deleted and listed", res)
	}

	requireSweepEntry(t, r.audit.only(t))
	requireStepOutput(t, &res, want[0])
}

// requireSweepEntry checks the audit entry of a run that deleted the chunk.
func requireSweepEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	if e.Action != job.ArtifactSweepAuditAction || e.Namespace != audit.ServerScope ||
		!strings.HasPrefix(e.Reason, job.ArtifactSweepReason+": ") || !strings.Contains(e.Reason, "1 blobs deleted") {
		t.Fatalf("audit entry = %+v, want the sweep, its reason and its counts", e)
	}

	if strings.Contains(e.Reason, chunkKey) {
		t.Fatalf("audit reason %q names a blob key", e.Reason)
	}
}

// requireStepOutput checks the JSON of the result, the output of the step.
func requireStepOutput(t *testing.T, res *job.ArtifactSweepResult, key string) {
	t.Helper()

	out, err := json.Marshal(res)
	if err != nil || !strings.Contains(string(out), `"deletedKeys":["`+key+`"]`) ||
		!strings.Contains(string(out), `"truncated":false`) {
		t.Fatalf("step output = %s, %v; want the deleted keys and the truncated flag", out, err)
	}
}

// TestSweepDeletesNothingOnClockSkew shows that a run whose instance clock
// differs from the clock of the database by more than the skew deletes
// nothing, and records why.
func TestSweepDeletesNothingOnClockSkew(t *testing.T) {
	t.Parallel()

	nothing := sweepNames{names: job.ArtifactNames{Plans: nil, ChunkKeys: nil}, err: nil}

	for _, skew := range []time.Duration{blob.AttemptSkew + time.Minute, -blob.AttemptSkew - time.Minute} {
		r := newSweepRig(t, sweepSetup{names: nothing, ahead: sweepAhead, instance: skew, wrap: nil})

		res, err := r.s.Sweep(t.Context())
		if err != nil || res.Deleted != 0 || res.Jobs != 0 || !strings.Contains(res.Skipped, "do not agree") {
			t.Errorf("instance %s off: Sweep = %+v, %v; want nothing swept and the reason", skew, res, err)
		}

		if e := r.audit.only(t); !strings.HasPrefix(e.Reason, "deleted nothing: ") {
			t.Errorf("instance %s off: audit reason = %q, want the reason that it deleted nothing", skew, e.Reason)
		}

		if _, err := r.s.SweepJob(t.Context(), &r.jobs.j); !errors.Is(err, blob.ErrClockSkew) {
			t.Errorf("instance %s off: SweepJob = %v, want ErrClockSkew", skew, err)
		}

		requireStored(t, r.store, chunkKey, planPart)
	}
}

// zeroTimes is a blob store whose list gives no modification time.
type zeroTimes struct{ blob.Store }

func (z zeroTimes) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	page, err := z.Store.ListPage(ctx, req)
	if err != nil {
		return blob.Page{}, fmt.Errorf("list page: %w", err)
	}

	for i := range page.Objects {
		page.Objects[i].ModifiedAt = time.Time{}
	}

	return page, nil
}

// TestSweepKeepsABlobWithNoModificationTime shows that a blob with no
// modification time is never old: the sweep keeps it and counts it unknown.
func TestSweepKeepsABlobWithNoModificationTime(t *testing.T) {
	t.Parallel()

	nothing := sweepNames{names: job.ArtifactNames{Plans: nil, ChunkKeys: nil}, err: nil}
	r := newSweepRig(t, sweepSetup{
		names:    nothing,
		ahead:    sweepAhead,
		instance: 0,
		wrap:     func(s blob.Store) blob.Store { return zeroTimes{Store: s} },
	})

	res, err := r.s.SweepJob(t.Context(), &r.jobs.j)
	if err != nil || res.Deleted != 0 || res.Unknown != 2 {
		t.Fatalf("SweepJob = %+v, %v; want nothing deleted and 2 unknown", res, err)
	}

	requireStored(t, r.store, chunkKey, planPart)
}
