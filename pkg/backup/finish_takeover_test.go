package backup_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/jobcheck"
)

// finishGate holds a succeeded finish before the row write, after the caller
// wrote the live pointer. The hook runs inside FinishBackup of the fake
// registry, so the gate needs no extra port method here.
type finishGate struct {
	entered chan struct{}
	release chan struct{}
	done    <-chan struct{}
	gated   atomic.Bool
}

// gateRegistry wraps the fake registry of the durable setup. Its gates stop a
// holder at the two points of the finish flow that a takeover hits: after the
// write step recorded its attempt, and between the live pointer write and the
// finish commit. A gate holds the holder until the test releases it or the
// test ends.
type gateRegistry struct {
	*fakeRegistry

	testDone <-chan struct{}

	// attemptEntered closes when the write step recorded its attempt. The
	// holder then waits on attemptRelease.
	attemptEntered chan struct{}
	attemptRelease chan struct{}

	// finish stops a succeeded finish before the row write, after the live
	// pointer is in the target, as an instance that stopped between the
	// pointer write and the commit.
	finish *finishGate
}

// RecordBackupAttempt stores the attempt and then holds the holder, so the
// test can expire its claim after the record committed.
func (g *gateRegistry) RecordBackupAttempt(
	ctx context.Context, fence durable.StepFence, id backup.ID, attempt string,
) error {
	err := g.fakeRegistry.RecordBackupAttempt(ctx, fence, id, attempt)

	if err == nil && g.attemptEntered != nil {
		close(g.attemptEntered)

		select {
		case <-g.attemptRelease:
		case <-g.testDone:
		}
	}

	return err
}

// armFinishGate holds the first succeeded finish of the fake registry of g
// before the row write, after the caller wrote the live pointer. The hook
// runs inside FinishBackup of the fake, so the gate needs no extra port
// method here.
func armFinishGate(g *gateRegistry, gate *finishGate) {
	gate.gated.Store(true)
	g.beforeFinish = func(state backup.State, _ string) {
		if state == backup.StateSucceeded && gate.gated.CompareAndSwap(true, false) {
			close(gate.entered)

			select {
			case <-gate.release:
			case <-gate.done:
			}
		}
	}
}

// gatedService builds one instance over the shared registry and target with a
// gate registry, as setup.service does with the plain registry. svcOpts
// carries the per-instance options; the setup carries the shared stores.
type gatedSvcOpts struct {
	reg   backup.Registry
	ops   durable.Store
	lease time.Duration
	owner string
}

// gatedService builds one instance over the shared stores of setup d.
func gatedService(t *testing.T, d durableSetup, opts gatedSvcOpts) *backup.Service {
	t.Helper()

	svc, err := backup.NewService(backup.Config{
		Jobs:        jobcheck.Checker{},
		Exporter:    &stubExporter{sum: testSummary(), data: tableData, err: nil},
		Kinds:       fixedKind(installation.KindProduction),
		Registry:    opts.reg,
		Operations:  opts.ops,
		Blobs:       d.source,
		StateOpener: nil,
		Target:      d.target,
		TargetName:  "mem:target",
		Now:         time.Now,
		Version:     "test-1.0",
		MaxSQLBytes: 0,
		Owner:       opts.owner,
		Lease:       opts.lease,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc
}

// TestFinishAfterTakeover records the write step under one claim and runs the
// finish step under a later claim. The finish step takes the attempt from the
// backups row, so it checks and commits the prefix of the row attempt, and
// the target holds no directory of the attempt of the new claim.
func TestFinishAfterTakeover(t *testing.T) {
	t.Parallel()

	const lease = 2 * time.Second

	setup := newDurableSetup()
	id := backup.ID("bkp-finish-takeover")

	reg := &gateRegistry{
		fakeRegistry:   setup.reg,
		testDone:       t.Context().Done(),
		attemptEntered: make(chan struct{}),
		attemptRelease: make(chan struct{}),
		finish:         nil,
	}

	// The instance A holds the claim and stops inside the record of the
	// write step, after the record committed.
	storeA := &frozenStore{Store: setup.reg.ops, frozen: atomic.Bool{}}
	svcA := gatedService(t, setup, gatedSvcOpts{reg: reg, ops: storeA, lease: lease, owner: "instance-a"})
	workerA := startWorker(t, svcA)

	if err := svcA.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	<-reg.attemptEntered

	row, err := setup.reg.GetBackup(t.Context(), id)
	if err != nil || row.Attempt == "" {
		t.Fatalf("row after the recorded write step = %+v, %v, want a recorded attempt", row, err)
	}

	// A stops renewing: its claim expires for real, while it believes it
	// holds it. The instance B takes the operation over and runs only the
	// finish step, under its own, later claim.
	storeA.frozen.Store(true)

	svcB := setup.service(
		t,
		&stubExporter{sum: testSummary(), data: tableData, err: nil},
		setup.reg.ops,
		lease,
		"instance-b",
	)
	startWorker(t, svcB)

	finished := waitRow(t, setup.reg, id, backup.StateSucceeded)
	if finished.Attempt != row.Attempt {
		t.Fatalf(
			"attempt of the finished row = %q, want the recorded attempt %q: the row decides the live attempt",
			finished.Attempt, row.Attempt,
		)
	}

	op := waitOperation(t, setup.reg.ops, id, "the operation succeeded", func(o durable.Operation) bool {
		return o.State == durable.StateSucceeded
	})
	if op.Attempt < 2 {
		t.Fatalf("operation attempts = %d, want a takeover", op.Attempt)
	}

	// The finish checked and committed the prefix of the row attempt: the
	// target holds that one directory, the pointer that names it and nothing
	// else, also no directory of the attempt of the later claim.
	assertCompleteBackup(t, setup.target, id)

	if got := setup.target.putAttempts(livePointerKeyOf(id)); got != 1 {
		t.Fatalf("puts of the live pointer = %d, want 1: the holder that took over wrote it", got)
	}

	// A resumes after B finished. Its late writes change nothing.
	close(reg.attemptRelease)
	waitFor(t, "the stale holder resumed and stopped", func() bool {
		_, resumedErr := setup.reg.GetBackup(t.Context(), id)
		return resumedErr == nil
	})
	workerA.stop()

	after, err := setup.reg.GetBackup(t.Context(), id)
	if err != nil || after != finished {
		t.Fatalf("row after the stale holder resumed = %+v, %v, want unchanged %+v", after, err, finished)
	}

	assertCompleteBackup(t, setup.target, id)
}

// TestFinishAfterPointerWriteBeforeCommit stops the holder between the live
// pointer write and the finish commit. The next holder finds the pointer
// equal to the row attempt, writes no pointer and commits.
func TestFinishAfterPointerWriteBeforeCommit(t *testing.T) {
	t.Parallel()

	const lease = 2 * time.Second

	setup := newDurableSetup()
	id := backup.ID("bkp-finish-pointer")

	reg := &gateRegistry{
		fakeRegistry:   setup.reg,
		testDone:       t.Context().Done(),
		attemptEntered: nil,
		attemptRelease: nil,
		finish: &finishGate{
			entered: make(chan struct{}),
			release: make(chan struct{}),
			done:    t.Context().Done(),
			gated:   atomic.Bool{},
		},
	}

	// Gate the first succeeded finish only: the one of the instance A.
	armFinishGate(reg, reg.finish)

	// The instance A reaches the finish commit with the pointer already in
	// the target, and stops there.
	storeA := &frozenStore{Store: setup.reg.ops, frozen: atomic.Bool{}}
	svcA := gatedService(t, setup, gatedSvcOpts{reg: reg, ops: storeA, lease: lease, owner: "instance-a"})
	workerA := startWorker(t, svcA)

	if err := svcA.Start(t.Context(), id); err != nil {
		t.Fatalf("start backup: %v", err)
	}

	<-reg.finish.entered

	if !setup.target.has(livePointerKeyOf(id)) {
		t.Fatal("the live pointer is not in the target at the finish commit")
	}

	// A stops renewing, its claim expires, and the instance B runs the finish
	// step again: the pointer names the row attempt, so B writes no pointer
	// and commits.
	storeA.frozen.Store(true)

	svcB := setup.service(
		t,
		&stubExporter{sum: testSummary(), data: tableData, err: nil},
		setup.reg.ops,
		lease,
		"instance-b",
	)
	startWorker(t, svcB)

	finished := waitRow(t, setup.reg, id, backup.StateSucceeded)
	if finished.Attempt == "" {
		t.Fatalf("finished row records no attempt: %+v", finished)
	}

	if got := setup.target.putAttempts(livePointerKeyOf(id)); got != 1 {
		t.Fatalf("puts of the live pointer = %d, want 1: the next holder found it equal", got)
	}

	assertCompleteBackup(t, setup.target, id)

	// A resumes: the commit refuses its dead claim and changes nothing.
	close(reg.finish.release)
	waitFor(t, "the refused finish of the first instance", func() bool { return setup.reg.lostFinishes.Load() == 1 })
	workerA.stop()

	after, err := setup.reg.GetBackup(t.Context(), id)
	if err != nil || after != finished {
		t.Fatalf("row after the stale holder resumed = %+v, %v, want unchanged %+v", after, err, finished)
	}

	assertCompleteBackup(t, setup.target, id)
}
