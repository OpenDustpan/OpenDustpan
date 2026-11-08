package storetest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// RestoreAreaDropper is the capability of a store test to remove a staging
// area as the cleanup of the store does, with no record: the state of a
// holder that stopped between the removals of its cleanup and the record of
// the step. Each SQL adapter implements it in its test.
type RestoreAreaDropper interface {
	DropRestoreArea(ctx context.Context, a backup.StagingArea) error
}

// restoreCrashCases are the worker cases of a holder that stops inside a
// step, before the fenced write that records it, and the cases of the end of
// a restore that stopped: its audit entries and its fence of the namespaces.
func restoreCrashCases() []restoreCase {
	return []restoreCase{
		{
			name: "a takeover after the enter write, with raced work, lists the work again and audits each cancel once",
			test: testCrashAfterEnterWrite,
		},
		{
			name: "a takeover after the enter write enters again when an operator left maintenance mode",
			test: testCrashAfterEnterOperatorLeft,
		},
		{name: "a takeover before the audit append writes the entries once", test: testCrashBeforeAuditAppend},
		{name: "a takeover after the removals of the cleanup ends the restore once", test: testCrashInCleanup},
		{
			name: "a takeover after the over-limit event of the unverified keys keeps one event",
			test: testCrashAfterUnverifiedEvent,
		},
		{name: "a failure after the swap writes the restore audit entry once", test: testFailureAfterSwapAudits},
		{name: "a cancel in the check step fences no namespace", test: testCancelInCheckFencesNothing},
		{name: "a canceled check-only restore fences no namespace", test: testCanceledCheckOnlyFencesNothing},
		{name: "a cancel after the enter fences every namespace once", test: testCancelAfterEnterFences},
		{
			name: "a restore that stopped after a forced cancel and before the confirm is fenced once",
			test: testStopAfterForcedCancelFences,
		},
	}
}

// crashPoint says where crashingLive stops its holder.
type crashPoint int

const (
	// crashAfterEnterWrite stops the holder after its fenced write of the
	// state maintenance, before the confirm records the step.
	crashAfterEnterWrite crashPoint = iota + 1
	// crashBeforeAuditAppend stops the holder before the fenced append of
	// the audit entries of after-apply.
	crashBeforeAuditAppend
	// crashInCleanup removes the staging areas of the restores that ended,
	// with no record, and stops the holder before the fenced cleanup.
	crashInCleanup
	// crashAfterCancelEvent stops the holder after it stored the audit event
	// of a forced cancel, before its write of maintenance mode.
	crashAfterCancelEvent
	// crashAfterUnverifiedEvent stops the holder after it stored the audit
	// event that names the unverified keys of the backup, before the record
	// of the copy-blobs step.
	crashAfterUnverifiedEvent
)

// unverifiedEventObjectPart is a fixed part of the object text of the audit
// event that names the unverified keys of a backup over the bound. The crash
// after that event matches only this object.
const unverifiedEventObjectPart = "blobs without a size and an MD5"

// crashingLive is the live store of one instance. It stops the holder at one
// fenced write of the domain port, once: the write before it committed, the
// fenced write itself does not run.
type crashingLive struct {
	backup.RestoreLive

	gate    *stepGate
	at      crashPoint
	dropper RestoreAreaDropper
	fired   *atomic.Bool
}

// SetRestoreMaintenance implements backup.RestoreOperations.
func (c crashingLive) SetRestoreMaintenance(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, expected, next maintenance.State,
) error {
	if err := c.RestoreLive.SetRestoreMaintenance(ctx, f, id, expected, next); err != nil {
		return fmt.Errorf("set restore maintenance: %w", err)
	}

	if c.at == crashAfterEnterWrite && next == maintenance.StateMaintenance && c.fired.CompareAndSwap(false, true) {
		c.gate.crash()

		return durable.ErrClaimLost
	}

	return nil
}

// RecordRestoreEvent implements backup.RestoreOperations.
func (c crashingLive) RecordRestoreEvent(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, object string,
) error {
	if err := c.RestoreLive.RecordRestoreEvent(ctx, f, id, object); err != nil {
		return fmt.Errorf("record restore event: %w", err)
	}

	if c.at == crashAfterCancelEvent && c.fired.CompareAndSwap(false, true) {
		c.gate.crash()

		return durable.ErrClaimLost
	}

	if c.at == crashAfterUnverifiedEvent && strings.Contains(object, unverifiedEventObjectPart) &&
		c.fired.CompareAndSwap(false, true) {
		c.gate.crash()

		return durable.ErrClaimLost
	}

	return nil
}

// AppendRestoreAudit implements backup.RestoreOperations.
func (c crashingLive) AppendRestoreAudit(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, entries []audit.Entry,
) ([]audit.Entry, error) {
	if c.at == crashBeforeAuditAppend && c.fired.CompareAndSwap(false, true) {
		c.gate.crash()

		return nil, durable.ErrClaimLost
	}

	out, err := c.RestoreLive.AppendRestoreAudit(ctx, f, id, entries)
	if err != nil {
		return nil, fmt.Errorf("append restore audit: %w", err)
	}

	return out, nil
}

// CleanRestoreAreas implements backup.RestoreStaging.
func (c crashingLive) CleanRestoreAreas(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, lockWait time.Duration,
) ([]backup.StagingArea, error) {
	if c.at == crashInCleanup && c.fired.CompareAndSwap(false, true) {
		areas, err := c.ListRestoreAreas(ctx)
		if err != nil {
			return nil, fmt.Errorf("list restore areas: %w", err)
		}

		for _, a := range areas {
			if err := c.dropper.DropRestoreArea(ctx, a); err != nil {
				return nil, fmt.Errorf("drop restore area %s: %w", a, err)
			}
		}

		c.gate.crash()

		return nil, durable.ErrClaimLost
	}

	dropped, err := c.RestoreLive.CleanRestoreAreas(ctx, f, id, lockWait)
	if err != nil {
		return nil, fmt.Errorf("clean restore areas: %w", err)
	}

	return dropped, nil
}

// racingMaintenance is the maintenance of the crash cases: each enter and
// each second list finds one unit of active work that raced it, and cancels
// it after the check of the writer, as the domain service does.
type racingMaintenance struct {
	inner    StateMaintenance
	enters   *atomic.Int32
	rechecks *atomic.Int32
}

// Get implements backup.RestoreMaintenance.
func (m racingMaintenance) Get(ctx context.Context) (maintenance.State, error) {
	return m.inner.Get(ctx)
}

// EnterWith implements backup.RestoreMaintenance.
func (m racingMaintenance) EnterWith(
	ctx context.Context, opts maintenance.EnterOptions,
) (maintenance.EnterResult, error) {
	n := m.enters.Add(1)
	unit := "stack acme/web update enter-" + strconv.Itoa(int(n))

	if err := opts.Writer.Check(ctx, unit); err != nil {
		return maintenance.EnterResult{Canceled: nil}, fmt.Errorf("check before the cancel: %w", err)
	}

	if _, err := m.inner.EnterWith(ctx, opts); err != nil {
		return maintenance.EnterResult{Canceled: nil}, err
	}

	return maintenance.EnterResult{Canceled: []string{unit}}, nil
}

// RecheckWith implements backup.RestoreMaintenance.
func (m racingMaintenance) RecheckWith(
	ctx context.Context, opts maintenance.EnterOptions,
) (maintenance.EnterResult, error) {
	n := m.rechecks.Add(1)
	unit := "stack acme/web update recheck-" + strconv.Itoa(int(n))

	if err := opts.Writer.Check(ctx, unit); err != nil {
		return maintenance.EnterResult{Canceled: nil}, fmt.Errorf("check before the cancel: %w", err)
	}

	return maintenance.EnterResult{Canceled: []string{unit}}, nil
}

// LeaveWith implements backup.RestoreMaintenance.
func (m racingMaintenance) LeaveWith(ctx context.Context, w maintenance.StateWriter) error {
	return m.inner.LeaveWith(ctx, w)
}

// Held implements backup.RestoreMaintenance.
func (m racingMaintenance) Held(ctx context.Context) (bool, error) {
	return m.inner.Held(ctx)
}

// crashRig is a restore whose instance a stops at one fenced write of the
// domain port, and the instance b that takes it over.
type crashRig struct {
	r        *workerRig
	id       backup.RestoreID
	a, b     *backup.RestoreService
	aDone    <-chan struct{}
	gate     *stepGate
	enters   *atomic.Int32
	rechecks *atomic.Int32
}

// newCrashRig starts a restore on instance a, which stops at at, and waits
// until it stopped.
func newCrashRig(t *testing.T, r *workerRig, at crashPoint, dropper RestoreAreaDropper) *crashRig {
	t.Helper()

	gate := newStepGate(t, r, "", gateNone)
	enters, rechecks := &atomic.Int32{}, &atomic.Int32{}
	maint := racingMaintenance{inner: StateMaintenance{State: r.st, Hold: false}, enters: enters, rechecks: rechecks}
	live := crashingLive{RestoreLive: r.st, gate: gate, at: at, dropper: dropper, fired: &atomic.Bool{}}

	a := r.service(t, workerService{
		owner: "instance-a", ops: gate, live: live, maint: maint, blobs: nil, lockWait: 0, afterApply: nil,
	})
	b := r.service(t, workerService{
		owner: "instance-b", ops: nil, live: nil, maint: maint, blobs: nil, lockWait: 0, afterApply: nil,
	})

	c := &crashRig{r: r, id: "", a: a, b: b, aDone: nil, gate: gate, enters: enters, rechecks: rechecks}
	c.id = r.start(t, a, false, false)
	c.aDone = runAsync(t, a, gate)
	<-c.aDone
	waitReached(t, gate, c.aDone)

	return c
}

// canceledEntries counts the audit entries of the cancel of unit by the
// restore of c with the claim token.
func (c *crashRig) canceledEntries(t *testing.T, token int64, unit string) int {
	t.Helper()

	return c.r.restoreEntries(t, "restore "+c.id.String()+" claim token "+strconv.FormatInt(token, 10)+
		" attempted to cancel the active work: "+unit)
}

func testCrashAfterEnterWrite(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	c := newCrashRig(t, r, crashAfterEnterWrite, nil)
	held := r.opOf(t, c.id)

	// The holder stopped after its write of maintenance mode: the flag of
	// the row is set, and the step is not recorded.
	mustState(t, r.st, maintenance.StateMaintenance)

	if _, done := held.Step(backup.RestoreStepEnterMaintenance); done {
		t.Fatal("the enter step is recorded, want the holder stopped before the confirm")
	}

	r.driveToEnd(t, c.b, c.id)
	r.mustRestoredOnce(t, c.id, restoredCheck{areas: true})

	if c.enters.Load() != 1 || c.rechecks.Load() != 1 {
		t.Fatalf("enters %d, second lists %d; want one of each: the takeover lists the raced work again",
			c.enters.Load(), c.rechecks.Load())
	}

	enter, ok := r.opOf(t, c.id).Step(backup.RestoreStepEnterMaintenance)
	if !ok {
		t.Fatal("the restore recorded no enter step")
	}

	result, err := backup.DecodeRestoreMaintenance(enter.Result)
	if err != nil {
		t.Fatalf("decode the enter result: %v", err)
	}

	// The cancel of the stopped holder and the cancel of the new holder each
	// have one audit entry, with the token of the holder that canceled.
	if n := c.canceledEntries(t, held.Claim.Token, "stack acme/web update enter-1"); n != 1 {
		t.Fatalf("audit entries of the cancel of the stopped holder = %d, want 1", n)
	}

	if n := c.canceledEntries(t, result.Token, "stack acme/web update recheck-1"); n != 1 {
		t.Fatalf("audit entries of the cancel of the new holder = %d, want 1", n)
	}
}

func testCrashAfterEnterOperatorLeft(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	c := newCrashRig(t, r, crashAfterEnterWrite, nil)

	// An operator leaves the maintenance mode that the stopped holder
	// entered. The flag of the row stays set.
	if err := r.st.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("leave maintenance mode as an operator: %v", err)
	}

	r.driveToEnd(t, c.b, c.id)
	r.mustRestoredOnce(t, c.id, restoredCheck{areas: true})

	if c.enters.Load() != 2 || c.rechecks.Load() != 0 {
		t.Fatalf("enters %d, second lists %d; want two enters and no second list", c.enters.Load(), c.rechecks.Load())
	}
}

func testCrashBeforeAuditAppend(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	c := newCrashRig(t, r, crashBeforeAuditAppend, nil)

	held := r.opOf(t, c.id)
	if _, done := held.Step(backup.RestoreStepAfterApply); done {
		t.Fatal("the after-apply step is recorded, want the holder stopped before the append")
	}

	r.driveToEnd(t, c.b, c.id)
	r.mustRestoredOnce(t, c.id, restoredCheck{areas: true})

	// The stopped holder entered maintenance mode and canceled the raced
	// work: the new holder writes that entry once, with the restore entry.
	if n := c.canceledEntries(t, held.Claim.Token, "stack acme/web update enter-1"); n != 1 {
		t.Fatalf("audit entries of the cancel = %d, want 1", n)
	}
}

func testCrashInCleanup(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	// A restore that a cancel stopped before the swap leaves its staging
	// area for the cleanup of the next restore.
	r, first, _ := cancelBefore(t, newStore, cancelSpec{step: backup.RestoreStepSwap, checkOnly: false, maintOf: nil})

	dropper, ok := r.st.(RestoreAreaDropper)
	if !ok {
		t.Fatal("the store test does not implement storetest.RestoreAreaDropper")
	}

	if len(r.areasOf(t, first)) == 0 {
		t.Fatalf("restore %s canceled before the swap left no staging area", first)
	}

	c := newCrashRig(t, r, crashInCleanup, dropper)

	if _, done := r.opOf(t, c.id).Step(backup.RestoreStepCleanup); done {
		t.Fatal("the cleanup step is recorded, want the holder stopped between the removals and the record")
	}

	r.driveToEnd(t, c.b, c.id)
	r.mustRestoredOnce(t, c.id, restoredCheck{areas: true})

	if areas := r.areasOf(t, first); len(areas) != 0 {
		t.Fatalf("staging areas of the ended restore %s = %v, want none", first, areas)
	}
}

// testCrashAfterUnverifiedEvent stops the holder after it stored the audit
// event that names the unverified keys of a backup over the bound. The
// takeover runs the copy-blobs step again, which stores the same object: the
// restore keeps one such event.
func testCrashAfterUnverifiedEvent(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)

	// The manifest names one key more than the bound on the unverified keys,
	// as the manifest of an earlier version of the server does.
	fillUnverified(t, r.target, r.backupID, backup.MaxUnverifiedKeys+1)

	c := newCrashRig(t, r, crashAfterUnverifiedEvent, nil)

	// The holder stopped after it stored the event, before the record of its
	// step: the event is on the row, and the takeover runs the step again.
	if _, done := r.opOf(t, c.id).Step(backup.RestoreStepCopyBlobs); done {
		t.Fatal("the copy-blobs step is recorded, want the holder stopped before the confirm")
	}

	if noted := r.unverifiedEvents(t, c.id); len(noted) != 1 {
		t.Fatalf("audit events that name the unverified keys after the stop = %v, want one", noted)
	}

	r.driveToEnd(t, c.b, c.id)
	r.mustRestoredOnce(t, c.id, restoredCheck{areas: true})

	if noted := r.unverifiedEvents(t, c.id); len(noted) != 1 {
		t.Fatalf("audit events that name the unverified keys after the takeover = %v, want one", noted)
	}
}

// unverifiedEvents returns the audit events of restore id that name the
// unverified keys of the backup.
func (r *workerRig) unverifiedEvents(t *testing.T, id backup.RestoreID) []string {
	t.Helper()

	events, err := r.st.RestoreEvents(t.Context(), id)
	if err != nil {
		t.Fatalf("read the restore events: %v", err)
	}

	var noted []string

	for _, object := range events {
		if strings.Contains(object, unverifiedEventObjectPart) {
			noted = append(noted, object)
		}
	}

	return noted
}

// errHookFailed is the error of the after-apply hook that fails.
var errHookFailed = errors.New("after-apply hook failed")

func testFailureAfterSwapAudits(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	a := r.service(t, workerService{
		owner: "instance-a", ops: nil, live: nil, maint: nil, blobs: nil, lockWait: 0,
		afterApply: func(context.Context) error { return errHookFailed },
	})

	id := r.start(t, a, false, false)

	row := r.driveToEnd(t, a, id)
	if row.State != backup.RestoreFailed {
		t.Fatalf("restore with a failing hook = %q, want failed", row.State)
	}

	// The data is swapped, and the swap replaced the audit log: the end of
	// the restore wrote its entry into the restored log, once.
	if r.hasLater(t) {
		t.Fatalf("namespace %s survived the swap", laterNamespace)
	}

	if n := r.restoreEntries(t, "restore "+id.String()+" of backup "); n != 1 {
		t.Fatalf("audit entries of restore %s = %d, want 1", id, n)
	}

	mustState(t, r.st, maintenance.StateNormal)
}

func testCancelInCheckFencesNothing(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r, _, _ := cancelBefore(t, newStore, cancelSpec{step: backup.RestoreStepCheck, checkOnly: false, maintOf: nil})

	if got := r.failoverVersion(t); got != r.failover {
		t.Fatalf("failover version after a cancel in check = %d, want %d", got, r.failover)
	}

	r.mustUnchanged(t)
}

func testCanceledCheckOnlyFencesNothing(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r, _, _ := cancelBefore(t, newStore, cancelSpec{step: backup.RestoreStepSwap, checkOnly: true, maintOf: nil})

	if got := r.failoverVersion(t); got != r.failover {
		t.Fatalf("failover version after a canceled check-only restore = %d, want %d", got, r.failover)
	}

	r.mustUnchanged(t)
}

func testCancelAfterEnterFences(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r, _, _ := cancelBefore(t, newStore, cancelSpec{step: backup.RestoreStepSwap, checkOnly: false, maintOf: nil})

	if got := r.failoverVersion(t); got != r.failover+1 {
		t.Fatalf("failover version after a cancel after the enter = %d, want %d", got, r.failover+1)
	}

	r.mustUnchanged(t)
	mustState(t, r.st, maintenance.StateNormal)
}

func testStopAfterForcedCancelFences(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	r := newWorkerRig(t, newStore)
	before := r.failoverVersion(t)
	c := newCrashRig(t, r, crashAfterCancelEvent, nil)
	held := r.opOf(t, c.id)

	// The holder stored the cancel of the raced work and stopped before its
	// write of maintenance mode: no enter step, no flag, one event.
	mustState(t, r.st, maintenance.StateNormal)

	if entered, err := r.st.RestoreMaintenanceEntered(t.Context(), c.id); err != nil || entered {
		t.Fatalf("maintenance flag = %t, %v; want false", entered, err)
	}

	if err := c.b.Cancel(t.Context(), c.id); err != nil {
		t.Fatalf("cancel restore %s: %v", c.id, err)
	}

	row := r.driveToEnd(t, c.b, c.id)
	if row.State != backup.RestoreFailed || row.Error != backup.RestoreCanceledText {
		t.Fatalf("restore = %q (%s), want failed with %q", row.State, row.Error, backup.RestoreCanceledText)
	}

	if got := r.failoverVersion(t); got != before+1 {
		t.Fatalf("failover version = %d, want %d: the sweep fences once after a forced cancel", got, before+1)
	}

	if n := c.canceledEntries(t, held.Claim.Token, "stack acme/web update enter-1"); n != 1 {
		t.Fatalf("audit entries of the forced cancel = %d, want 1", n)
	}

	r.mustUnchanged(t)
}
