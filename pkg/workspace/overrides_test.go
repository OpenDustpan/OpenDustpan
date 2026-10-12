package workspace_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestUnlockedDeclareNeedsTheSwitches proves a declare on an unlocked unit is
// refused unless the caller may use the unlocked-write override. The refusal
// stays ErrNotLocked, and an allowed declare finalizes without a lock (ADR 44,
// decisions 5 and 8).
func TestUnlockedDeclareNeedsTheSwitches(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	in := newState("lin-old", 1, smallDoc)

	_, err := f.svc.BeginState(ctx, f.ws.ID, "alice", in, forcedOnly())
	if !errors.Is(err, workspace.ErrNotLocked) {
		t.Fatalf("declare on an unlocked unit: err = %v, want ErrNotLocked", err)
	}

	if !strings.Contains(err.Error(), "unit:write-unlocked") {
		t.Fatalf("refusal does not name the action: %v", err)
	}

	// The action alone is not enough: the switches must be on too, and the
	// store refuses the declare while either is off (ADR 44, decision 6).
	_, err = f.svc.BeginState(ctx, f.ws.ID, "alice", in, namespace.Overrides{Unlocked: true, Forced: false})
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("declare with the action but no switches: err = %v, want ErrOverrideRefused", err)
	}

	_, err = f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, namespace.Overrides{Unlocked: true, Forced: false})
	if err != nil {
		t.Fatalf("turn on the namespace switch: %v", err)
	}

	if _, err = f.svc.SetOverrides(ctx, f.ws.ID, namespace.Overrides{Unlocked: true, Forced: false}); err != nil {
		t.Fatalf("turn on the unit switch: %v", err)
	}

	pending, err := f.svc.BeginState(ctx, f.ws.ID, "alice", in, namespace.Overrides{Unlocked: true, Forced: false})
	if err != nil {
		t.Fatalf("declare with the unlocked override: %v", err)
	}

	if !pending.Version.UnlockedWrite {
		t.Fatal("version declared without the lock does not carry the unlocked-write flag")
	}

	if _, err = f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(smallDoc)); err != nil {
		t.Fatalf("upload of an unlocked write: %v", err)
	}

	cur, err := f.svc.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current state: %v", err)
	}

	if cur.ID != pending.Version.ID {
		t.Fatalf("current = %s, want the unlocked write %s", cur.ID, pending.Version.ID)
	}
}

// TestUnitSwitchNarrowsNamespaceSwitch proves an override needs both the
// namespace switch and the unit switch: a unit switch cannot widen a namespace
// that does not allow the override (ADR 44, decision 6).
func TestUnitSwitchNarrowsNamespaceSwitch(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	on := namespace.Overrides{Forced: true, Unlocked: true}

	if _, err := f.svc.SetOverrides(ctx, f.ws.ID, on); err != nil {
		t.Fatalf("turn on the unit switches: %v", err)
	}

	got, err := f.svc.Overrides(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}

	if got != noOverrides() {
		t.Fatalf("unit switch on, namespace off: overrides = %+v, want none", got)
	}

	if _, err = f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, on); err != nil {
		t.Fatalf("turn on the namespace switches: %v", err)
	}

	if got, err = f.svc.Overrides(ctx, f.ws.ID); err != nil || got != on {
		t.Fatalf("both switches on: overrides = %+v, err = %v, want %+v", got, err, on)
	}

	if _, err = f.svc.SetOverrides(ctx, f.ws.ID, forcedOnly()); err != nil {
		t.Fatalf("narrow the unit switches: %v", err)
	}

	got, err = f.svc.Overrides(ctx, f.ws.ID)
	if err != nil || got != forcedOnly() {
		t.Fatalf("unit narrows to forced: overrides = %+v, err = %v", got, err)
	}
}

// TestForceUnlockClientEncryptedWithForcedOverride proves the forced-write
// override releases the lock of a client-encrypted state without a fence. No
// version is written, so the encrypted state stays current (ADR 44, decision 4).
func TestForceUnlockClientEncryptedWithForcedOverride(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	const encrypted = `{"encrypted_data":"c2VjcmV0"}`

	if err := f.push(t, newState("lin-old", 1, encrypted), encrypted); err != nil {
		t.Fatalf("push encrypted state: %v", err)
	}

	before, err := f.svc.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current before: %v", err)
	}

	if _, err = f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, forcedOnly()); err != nil {
		t.Fatalf("turn on the namespace switch: %v", err)
	}

	if _, err = f.svc.SetOverrides(ctx, f.ws.ID, namespace.Overrides{Forced: true, Unlocked: false}); err != nil {
		t.Fatalf("turn on the unit switch: %v", err)
	}

	released, overridden, err := f.svc.ForceUnlock(
		ctx,
		f.ws.ID,
		"admin",
		forcedOnly(),
		nil,
	)
	if err != nil {
		t.Fatalf("force unlock with the forced override: %v", err)
	}

	if !overridden {
		t.Fatal("unfenced release did not report the override")
	}

	if released.Lock.Locked() {
		t.Fatal("lock still held after the override release")
	}

	after, err := f.svc.Current(ctx, f.ws.ID)
	if err != nil {
		t.Fatalf("current after: %v", err)
	}

	if after.ID != before.ID {
		t.Fatalf("current = %s after an unfenced release, want the encrypted state %s", after.ID, before.ID)
	}
}

// forcedOnly returns the overrides that allow a forced write and no unlocked one.
func forcedOnly() namespace.Overrides {
	return namespace.Overrides{Forced: true, Unlocked: false}
}

// noOverrides returns the overrides that allow nothing, the default of a namespace and a unit.
func noOverrides() namespace.Overrides {
	return namespace.Overrides{Forced: false, Unlocked: false}
}

// TestForceUnlockStopsWhenItsAuditEntryFails proves that an unfenced release
// does not happen when its audit entry cannot be written: the lock stays held
// (ADR 41, decision 5).
func TestForceUnlockStopsWhenItsAuditEntryFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	const encrypted = `{"encrypted_data":"c2VjcmV0"}`

	if err := f.push(t, newState("lin-old", 1, encrypted), encrypted); err != nil {
		t.Fatalf("push encrypted state: %v", err)
	}

	errAudit := workspace.ErrConcurrentModification
	record := func() error { return errAudit }

	_, _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", forcedOnly(), record)
	if !errors.Is(err, errAudit) {
		t.Fatalf("force unlock with a failing audit entry: err = %v, want the audit error", err)
	}

	held, getErr := f.svc.GetByID(ctx, f.ws.ID)
	if getErr != nil {
		t.Fatalf("get workspace: %v", getErr)
	}

	if !held.Lock.Locked() {
		t.Fatal("the lock was released although its audit entry failed")
	}
}

// TestForcedDeclareNeedsTheActionAndSwitches proves a forced declare writes
// nothing without the unit:write-forced action, or while a switch is off. With
// both the action and the switches it is allowed (ADR 44, decisions 4 and 6).
func TestForcedDeclareNeedsTheActionAndSwitches(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	forced := newState("lin-old", 1, smallDoc)
	forced.Force = true

	_, err := f.svc.BeginState(ctx, f.ws.ID, "alice", forced, noOverrides())
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("forced declare without the action: err = %v, want ErrOverrideRefused", err)
	}

	if !strings.Contains(err.Error(), "unit:write-forced") {
		t.Fatalf("refusal does not name the action: %v", err)
	}

	if _, curErr := f.svc.Current(ctx, f.ws.ID); !errors.Is(curErr, workspace.ErrNoState) {
		t.Fatalf("current after a refused forced declare: err = %v, want ErrNoState", curErr)
	}

	// The action alone is not enough: the store refuses while the switches are off.
	_, err = f.svc.BeginState(ctx, f.ws.ID, "alice", forced, forcedOnly())
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("forced declare with the action and the switches off: err = %v, want ErrOverrideRefused", err)
	}

	enableForced(t, f)

	if _, err = f.svc.BeginState(ctx, f.ws.ID, "alice", forced, forcedOnly()); err != nil {
		t.Fatalf("forced declare with the action and both switches: %v", err)
	}
}
