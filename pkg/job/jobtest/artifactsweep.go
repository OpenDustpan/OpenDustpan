package jobtest

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/job"
)

// ArtifactSweepHarness is a recorder harness with the job.ArtifactKeyStore of
// the same database.
type ArtifactSweepHarness struct {
	RecorderHarness

	// Names is the job.ArtifactKeyStore under test.
	Names job.ArtifactKeyStore
}

// ArtifactSweepFactory returns a fresh harness for one case.
type ArtifactSweepFactory func(t *testing.T) ArtifactSweepHarness

// RunArtifactSweep executes the conformance suite of job.ArtifactKeyStore, and
// of job.ArtifactSweeper over it, against harnesses from newHarness. The
// reference adapter in memory, in the tests of this package, runs it first.
// Each SQL adapter runs it after.
func RunArtifactSweep(t *testing.T, newHarness ArtifactSweepFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h ArtifactSweepHarness)
	}{
		{"names: the plan rows and the confirmed chunks of a job are named", testArtifactNames},
		{"names: an unknown job and a job of another namespace are ErrNotFound", testArtifactNamesNotFound},
		{"sweep: an old blob that no row names goes, named and young blobs stay", testSweepDeletesOrphans},
		{"sweep: a running backup, a running restore or a failed guard read keeps every blob", testSweepHeld},
		{"sweep: a run sweeps each running job", testSweepRun},
		{"sweep: a chunk attempt whose confirm comes after the sweep of its blob is refused", testSweepStalledChunk},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

// guardSwitch is a blobguard.Watch whose answer a case sets.
type guardSwitch struct {
	work atomic.Pointer[blobguard.Work]
	fail atomic.Bool
}

// errGuardRead is the failure of a guard read in the cases.
var errGuardRead = errors.New("read of the backups registry failed")

// Running implements blobguard.Watch.
func (g *guardSwitch) Running(context.Context) (blobguard.Work, bool, error) {
	if g.fail.Load() {
		return blobguard.Work{Kind: "", ID: ""}, false, errGuardRead
	}

	if w := g.work.Load(); w != nil {
		return *w, true, nil
	}

	return blobguard.Work{Kind: "", ID: ""}, false, nil
}

// newGuardSwitch returns a guard that answers that nothing runs.
func newGuardSwitch() *guardSwitch {
	return &guardSwitch{work: atomic.Pointer[blobguard.Work]{}, fail: atomic.Bool{}}
}

// sweepAfter is how far ahead of the clock the sweep of the cases runs, so
// every blob of a case is older than the grace period.
const sweepAfter = blob.UnnamedGracePeriod + time.Minute

// newSweeper returns the sweep over the stores of h, with the guard g and a
// clock that is ahead by ahead.
func newSweeper(t *testing.T, h ArtifactSweepHarness, g blobguard.Watch, ahead time.Duration) *job.ArtifactSweeper {
	t.Helper()

	artifacts, err := job.NewArtifactBlobs(h.Blobs, noRefusals{})
	if err != nil {
		t.Fatalf("artifact blobs: %v", err)
	}

	s, err := job.NewArtifactSweeper(job.ArtifactSweeperConfig{
		Jobs: h.Jobs, Names: h.Names, Blobs: artifacts, Namespaces: h.Namespaces, Guard: g, Logger: nil,
		Now: func() time.Time { return time.Now().Add(ahead) },
	})
	if err != nil {
		t.Fatalf("artifact sweeper: %v", err)
	}

	return s
}

// sweepFixture is a job with a stored plan, a confirmed chunk 0 and a reserved
// chunk 1, and the blobs that no row names next to them.
type sweepFixture struct {
	a       *job.Assignment
	version string
	// named are the full keys that a row names.
	named []string
	// orphans are the full keys that no row names.
	orphans []string
	// unknown is the full key of a blob in the directory of the job that is
	// not an artifact blob key.
	unknown string
}

// newSweepFixture stores the plan, the chunks and the orphan blobs of a case.
func newSweepFixture(t *testing.T, h ArtifactSweepHarness) *sweepFixture {
	t.Helper()

	f := setUp(t, h.Harness)
	p := openPlan(t, h.RecorderHarness, f, "unit-0")
	plan := putPlan(t, h.RecorderHarness, p)

	a := logJob(t, h, f)
	mustAppend(t, h.RecorderHarness, a, 0, "line 0\n")
	version := logOf(t, h.RecorderHarness, a).ID

	reserveChunk(t, h, a, 1, "line 1\n")

	logNS, planNS := namespacePrefix(t, h, a), namespacePrefix(t, h, p)

	fx := &sweepFixture{a: a, version: version, named: nil, orphans: nil, unknown: ""}

	for _, rel := range chunkKeysOf(t, h, a, version) {
		fx.named = append(fx.named, logNS+rel)
	}

	fx.named = append(fx.named, planNS+planKey(t, p.Claim.Job, plan.ID, job.OutputPlanDisplay),
		planNS+planKey(t, p.Claim.Job, plan.ID, job.OutputPlanApply))

	fx.orphans = []string{
		// The loser of chunk 0 and a stalled attempt of the reserved chunk 1.
		logNS + attemptKey(t, a, version, 0, "lost"),
		logNS + attemptKey(t, a, version, 1, "stalled"),
		// The part of a plan whose row does not exist.
		logNS + planKey(t, a.Claim.Job, job.PlanIDPrefix+"gone", job.OutputPlanDisplay),
	}
	fx.unknown = logNS + "job/" + string(a.Claim.Job) + "/other/blob"

	for _, key := range append(slices.Clone(fx.orphans), fx.unknown) {
		putBlob(t, h, key)
	}

	return fx
}

// reserveChunk reserves the chunk seq of the step prepare with content, and
// confirms nothing.
func reserveChunk(t *testing.T, h ArtifactSweepHarness, a *job.Assignment, seq int, content string) {
	t.Helper()

	c := job.ChunkReserve{Seq: seq, SHA256: hashOf(content), Size: int64(len(content))}

	if _, err := h.Records.ReserveLogChunk(t.Context(), a.Claim, stepPrepare, c, nil); err != nil {
		t.Fatalf("ReserveLogChunk(%d) = %v", seq, err)
	}
}

// logJob opens an apply job on a second unit of the namespace of f, and begins
// its step prepare, as logCaseStep does in a namespace of its own.
func logJob(t *testing.T, h ArtifactSweepHarness, f fixture) *job.Assignment {
	t.Helper()

	unit, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "logs")
	if err != nil {
		t.Fatalf("CreateUnit: %v", err)
	}

	a := openApply(t, h.RecorderHarness, fixture{fence: f.fence, unit: unit}, "unit-1")
	mustBegin(t, h.RecorderHarness, a, stepPrepare)

	return a
}

// namespacePrefix returns the key prefix of the namespace of the job of a.
func namespacePrefix(t *testing.T, h ArtifactSweepHarness, a *job.Assignment) string {
	t.Helper()

	ns, err := h.Namespaces.GetNamespace(t.Context(), a.Claim.Fence.Namespace)
	if err != nil {
		t.Fatalf("GetNamespace = %v", err)
	}

	return "namespaces/" + ns.ID.String() + "/"
}

// chunkKeysOf returns the keys of the confirmed chunks of the version.
func chunkKeysOf(t *testing.T, h ArtifactSweepHarness, a *job.Assignment, version string) []string {
	t.Helper()

	rec, err := h.Records.Log(t.Context(), a.Claim.Fence.Namespace, version)
	if err != nil {
		t.Fatalf("Log = %v", err)
	}

	return rec.ChunkKeys
}

// planKey returns the key of the part kind of the plan of the job id.
func planKey(t *testing.T, id job.ID, plan string, kind job.OutputKind) string {
	t.Helper()

	key, err := job.PlanPartKey(id, plan, kind)
	if err != nil {
		t.Fatalf("PlanPartKey = %v", err)
	}

	return key.String()
}

// putBlob stores a small blob under the full key.
func putBlob(t *testing.T, h ArtifactSweepHarness, key string) {
	t.Helper()

	k, err := blob.ParseKey(key)
	if err != nil {
		t.Fatalf("ParseKey(%q) = %v", key, err)
	}

	if err := h.Blobs.Put(t.Context(), k, strings.NewReader("x")); err != nil {
		t.Fatalf("Put(%q) = %v", key, err)
	}
}

// stored reports whether the blob under the full key exists.
func stored(t *testing.T, h ArtifactSweepHarness, key string) bool {
	t.Helper()

	rc, err := h.Blobs.Get(t.Context(), blob.Key(key))
	if errors.Is(err, blob.ErrNotFound) {
		return false
	}

	if err != nil {
		t.Fatalf("Get(%q) = %v", key, err)
	}

	_ = rc.Close() //nolint:errcheck // A read-only test handle.

	return true
}

// requireBlobs checks that each key of keep is stored and that each key of gone
// is not.
func requireBlobs(t *testing.T, h ArtifactSweepHarness, keep, gone []string) {
	t.Helper()

	for _, key := range keep {
		if !stored(t, h, key) {
			t.Errorf("blob %q is gone, want it kept", key)
		}
	}

	for _, key := range gone {
		if stored(t, h, key) {
			t.Errorf("blob %q is stored, want it deleted", key)
		}
	}
}

// sweptJob returns the job of a.
func sweptJob(t *testing.T, h ArtifactSweepHarness, a *job.Assignment) job.Job {
	t.Helper()

	j, err := h.Jobs.Get(t.Context(), a.Claim.Job)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}

	return j
}

func testArtifactNames(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	f := setUp(t, h.Harness)
	p := openPlan(t, h.RecorderHarness, f, "unit-0")
	plan := putPlan(t, h.RecorderHarness, p)

	names, err := h.Names.ArtifactNames(t.Context(), p.Claim.Fence.Namespace, p.Claim.Job)
	if err != nil || !slices.Equal(names.Plans, []string{plan.ID}) || len(names.ChunkKeys) != 0 {
		t.Fatalf("ArtifactNames of the plan job = %+v, %v; want plan %s and no chunk", names, err, plan.ID)
	}

	a := logJob(t, h, f)
	mustAppend(t, h.RecorderHarness, a, 0, "line 0\n")
	version := logOf(t, h.RecorderHarness, a).ID

	reserveChunk(t, h, a, 1, "x")

	want := chunkKeysOf(t, h, a, version)

	names, err = h.Names.ArtifactNames(t.Context(), a.Claim.Fence.Namespace, a.Claim.Job)
	if err != nil || len(names.Plans) != 0 || !slices.Equal(sorted(names.ChunkKeys), sorted(want)) || len(want) != 1 {
		t.Fatalf("ArtifactNames of the log job = %+v, %v; want only the confirmed chunk %q", names, err, want)
	}
}

// sorted returns a sorted copy of keys.
func sorted(keys []string) []string {
	out := slices.Clone(keys)
	slices.Sort(out)

	return out
}

func testArtifactNamesNotFound(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	a := logCaseStep(t, h.RecorderHarness)

	other, err := h.Domain.CreateNamespace(t.Context(), "other-ns")
	if err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	cases := []struct {
		name string
		call func() error
	}{
		{"an unknown job", func() error {
			_, e := h.Names.ArtifactNames(t.Context(), a.Claim.Fence.Namespace, job.ID(job.IDPrefix+"unknown"))

			return e
		}},
		{"a job of another namespace", func() error {
			_, e := h.Names.ArtifactNames(t.Context(), other.Namespace, a.Claim.Job)

			return e
		}},
	}

	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, job.ErrNotFound) {
			t.Errorf("%s: ArtifactNames = %v, want ErrNotFound", tc.name, err)
		}
	}
}

func testSweepDeletesOrphans(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	fx := newSweepFixture(t, h)
	j := sweptJob(t, h, fx.a)
	g := newGuardSwitch()

	// With the clock of now, every blob is young, and the sweep keeps all.
	if res, err := newSweeper(t, h, g, 0).SweepJob(t.Context(), &j); err != nil || res.Deleted != 0 {
		t.Fatalf("young sweep = %+v, %v; want nothing deleted", res, err)
	}

	requireBlobs(t, h, append(slices.Clone(fx.named), fx.orphans...), nil)

	res, err := newSweeper(t, h, g, sweepAfter).SweepJob(t.Context(), &j)
	if err != nil || res.Deleted != len(fx.orphans) || res.Unknown != 1 {
		t.Fatalf("old sweep = %+v, %v; want %d deleted and 1 unknown", res, err, len(fx.orphans))
	}

	requireBlobs(t, h, append(slices.Clone(fx.named), fx.unknown), fx.orphans)

	// The named blobs still read back.
	rc, err := h.Reader.OpenLog(t.Context(), fx.a.Claim.Fence.Namespace, fx.version)
	if err != nil {
		t.Fatalf("OpenLog after the sweep = %v", err)
	}

	if _, err := io.ReadAll(rc); err != nil {
		t.Fatalf("read the log after the sweep = %v", err)
	}

	_ = rc.Close() //nolint:errcheck // A read-only test handle.
}

func testSweepHeld(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	fx := newSweepFixture(t, h)
	j := sweptJob(t, h, fx.a)

	cases := []struct {
		name string
		set  func(g *guardSwitch)
	}{
		{
			"a running backup",
			func(g *guardSwitch) { g.work.Store(&blobguard.Work{Kind: blobguard.Backup, ID: "bkp-1"}) },
		},
		{"a running restore", func(g *guardSwitch) {
			g.work.Store(&blobguard.Work{Kind: blobguard.Restore, ID: "rst-1"})
		}},
		{"a failed guard read", func(g *guardSwitch) { g.fail.Store(true) }},
		{
			"an unknown answer of the guard",
			func(g *guardSwitch) { g.work.Store(&blobguard.Work{Kind: "other", ID: "x"}) },
		},
	}

	for _, tc := range cases {
		g := newGuardSwitch()
		tc.set(g)

		s := newSweeper(t, h, g, sweepAfter)

		if res, err := s.SweepJob(t.Context(), &j); err == nil || res.Deleted != 0 {
			t.Errorf("%s: SweepJob = %+v, %v; want an error and nothing deleted", tc.name, res, err)
		}

		if res, err := s.Sweep(t.Context()); err != nil || !res.Held || res.Deleted != 0 {
			t.Errorf("%s: Sweep = %+v, %v; want held and nothing deleted", tc.name, res, err)
		}
	}

	requireBlobs(t, h, append(slices.Clone(fx.named), fx.orphans...), nil)
}

func testSweepRun(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	fx := newSweepFixture(t, h)

	res, err := newSweeper(t, h, newGuardSwitch(), sweepAfter).Sweep(t.Context())
	if err != nil || res.Held || res.Failed != 0 || res.Deleted != len(fx.orphans) {
		t.Fatalf("Sweep = %+v, %v; want %d deleted", res, err, len(fx.orphans))
	}

	requireBlobs(t, h, fx.named, fx.orphans)

	// The plan job and the log job both run, so the first pass lists both,
	// and the pass of the window lists both again.
	const runningJobs = 2

	if res.Jobs < runningJobs {
		t.Fatalf("Sweep swept %d jobs, want at least %d", res.Jobs, runningJobs)
	}
}

func testSweepStalledChunk(t *testing.T, h ArtifactSweepHarness) {
	t.Helper()

	fx := newSweepFixture(t, h)
	j := sweptJob(t, h, fx.a)

	// The attempt of the reserved chunk 1 wrote its blob and stalled. The sweep
	// deletes the blob once it is older than the grace period.
	stalled := fx.orphans[1]
	rel := stalled[len(namespacePrefix(t, h, fx.a)):]

	if _, err := newSweeper(t, h, newGuardSwitch(), sweepAfter).SweepJob(t.Context(), &j); err != nil {
		t.Fatalf("SweepJob = %v", err)
	}

	requireBlobs(t, h, nil, []string{stalled})

	// The stalled confirm arrives. Its attempt started before the grace
	// period, so the store refuses it and no row names the deleted blob.
	late := chunkAttempt{
		at: chunkAt{version: fx.version, seq: 1}, key: rel, started: time.Now().Add(-sweepAfter),
	}

	if won, err := confirmStarted(t, h.RecorderHarness, fx.a, late); won || !errors.Is(err, blob.ErrAttemptExpired) {
		t.Fatalf("stalled confirm = %v, %v; want false, ErrAttemptExpired", won, err)
	}

	names, err := h.Names.ArtifactNames(t.Context(), fx.a.Claim.Fence.Namespace, fx.a.Claim.Job)
	if err != nil {
		t.Fatalf("ArtifactNames = %v", err)
	}

	for _, key := range names.ChunkKeys {
		if !stored(t, h, namespacePrefix(t, h, fx.a)+key) {
			t.Errorf("a row names the missing blob %q", key)
		}
	}
}
