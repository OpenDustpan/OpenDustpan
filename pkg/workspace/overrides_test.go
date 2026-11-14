package workspace_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// requireRefusedOverride fails the test unless err matches ErrOverrideRefused and
// carries the typed refusal of want.
func requireRefusedOverride(t *testing.T, err error, want workspace.Override) {
	t.Helper()

	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("err = %v, want ErrOverrideRefused", err)
	}

	if got, ok := workspace.RefusedOverride(err); !ok || got != want {
		t.Fatalf("RefusedOverride = %v, %v, want %v", got, ok, want)
	}
}

// TestUnlockedDeclareNeedsTheSwitches proves a declare on an unlocked unit is
// refused unless the caller may use the unlocked-write override. The refusal
// stays ErrNotLocked, and an allowed declare finalizes without a lock (ADR 44,
// decisions 5 and 8).
func TestUnlockedDeclareNeedsTheSwitches(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	in := newState("lin-old", 1, smallDoc)

	_, err := f.svc.BeginState(ctx, f.ws.ID, "alice", in, authority(forcedOnly(), nil))
	if !errors.Is(err, workspace.ErrNotLocked) {
		t.Fatalf("declare on an unlocked unit: err = %v, want ErrNotLocked", err)
	}

	if !strings.Contains(err.Error(), "unit:write-unlocked") {
		t.Fatalf("refusal does not name the action: %v", err)
	}

	// The refusal also carries the typed signal that the adapters record.
	requireRefusedOverride(t, err, workspace.OverrideUnlocked)

	// The action alone is not enough: the switches must be on too, and the
	// store refuses the declare while either is off (ADR 44, decision 6).
	_, err = f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		in,
		authority(namespace.Overrides{Unlocked: true, Forced: false}, noRecord),
	)
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("declare with the action but no switches: err = %v, want ErrOverrideRefused", err)
	}

	requireRefusedOverride(t, err, workspace.OverrideUnlocked)

	_, err = f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, namespace.Overrides{Unlocked: true, Forced: false})
	if err != nil {
		t.Fatalf("turn on the namespace switch: %v", err)
	}

	if _, err = f.svc.SetOverrides(ctx, f.ws.ID, namespace.Overrides{Unlocked: true, Forced: false}); err != nil {
		t.Fatalf("turn on the unit switch: %v", err)
	}

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		in,
		authority(namespace.Overrides{Unlocked: true, Forced: false}, noRecord),
	)
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

	got, err := effectiveOverrides(t, f)
	if err != nil {
		t.Fatalf("overrides: %v", err)
	}

	if got != noOverrides() {
		t.Fatalf("unit switch on, namespace off: overrides = %+v, want none", got)
	}

	if _, err = f.namespaces.SetOverrides(ctx, f.ws.Ref.Namespace, on); err != nil {
		t.Fatalf("turn on the namespace switches: %v", err)
	}

	if got, err = effectiveOverrides(t, f); err != nil || got != on {
		t.Fatalf("both switches on: overrides = %+v, err = %v, want %+v", got, err, on)
	}

	if _, err = f.svc.SetOverrides(ctx, f.ws.ID, forcedOnly()); err != nil {
		t.Fatalf("narrow the unit switches: %v", err)
	}

	got, err = effectiveOverrides(t, f)
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

	if err := f.push(t, newState("lin-old", 1, envelope), envelope); err != nil {
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
		func() error { return nil },
		forceEntry(),
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

// forceEntry returns the audit entry template that a caller passes to a
// force-unlock: the request fields, a reason, and no field that the service sets.
func forceEntry() audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     "",
		Subject:       "",
		IDPSubject:    "",
		TokenID:       "tok-1",
		Action:        "unit:write-forced",
		Object:        "opentofu:acme/network",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "tofu/1.11.0",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-1",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "the run crashed",
		PrincipalKind: "",
		JobID:         "",
	}
}

// noOverrides returns the overrides that allow nothing, the default of a namespace and a unit.
func noOverrides() namespace.Overrides {
	return namespace.Overrides{Forced: false, Unlocked: false}
}

// noRecord is a record callback that writes no entry. A test that checks a
// refusal of the store passes it, because a write that uses an override
// needs a callback.
func noRecord(workspace.Override) error { return nil }

// authority returns the caller's side of a declare: the overrides it may use
// and the callback that records each override it uses.
func authority(allow namespace.Overrides, record func(workspace.Override) error) workspace.Authority {
	return workspace.Authority{Allow: allow, Record: record}
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

	if err := f.push(t, newState("lin-old", 1, envelope), envelope); err != nil {
		t.Fatalf("push encrypted state: %v", err)
	}

	errAudit := workspace.ErrConcurrentModification
	record := func() error { return errAudit }

	_, _, err := f.svc.ForceUnlock(ctx, f.ws.ID, "admin", forcedOnly(), record, forceEntry())
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

// TestForcedDeclareNeedsTheActionAndSwitches proves a forced declare into a unit
// that has state writes nothing without the unit:write-forced action, or while a
// switch is off. With both the action and the switches it is allowed (ADR 44,
// decisions 4 and 6).
func TestForcedDeclareNeedsTheActionAndSwitches(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
		t.Fatalf("first state: %v", err)
	}

	forced := newState("lin-old", 2, smallDoc)
	forced.Force = true

	_, err := f.svc.BeginState(ctx, f.ws.ID, "alice", forced, authority(noOverrides(), nil))
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("forced declare without the action: err = %v, want ErrOverrideRefused", err)
	}

	if !strings.Contains(err.Error(), "unit:write-forced") {
		t.Fatalf("refusal does not name the action: %v", err)
	}

	// The action alone is not enough: the store refuses while the switches are off.
	_, err = f.svc.BeginState(ctx, f.ws.ID, "alice", forced, authority(forcedOnly(), noRecord))
	if !errors.Is(err, workspace.ErrOverrideRefused) {
		t.Fatalf("forced declare with the action and the switches off: err = %v, want ErrOverrideRefused", err)
	}

	enableForced(t, f)

	var recorded []workspace.Override

	record := func(o workspace.Override) error {
		recorded = append(recorded, o)

		return nil
	}

	if _, err = f.svc.BeginState(ctx, f.ws.ID, "alice", forced, authority(forcedOnly(), record)); err != nil {
		t.Fatalf("forced declare with the action and both switches: %v", err)
	}

	if len(recorded) != 1 || recorded[0] != workspace.OverrideForced {
		t.Fatalf("recorded overrides = %v, want one forced write", recorded)
	}
}

// TestForcedDeclareIntoEmptyUnitNeedsNoOverride proves a forced declare into a unit
// with no state is allowed without any override: it overwrites nothing (ADR 44,
// decision 4).
func TestForcedDeclareIntoEmptyUnitNeedsNoOverride(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	forced := newState("lin-new", 1, smallDoc)
	forced.Force = true

	recorded := 0
	record := func(workspace.Override) error {
		recorded++

		return nil
	}

	if _, err := f.svc.BeginState(ctx, f.ws.ID, "alice", forced, authority(noOverrides(), record)); err != nil {
		t.Fatalf("forced declare into an empty unit: %v", err)
	}

	if recorded != 0 {
		t.Fatalf("an empty-unit forced write recorded %d overrides, want none", recorded)
	}
}

// TestDeclareRefusedWhenRecordFails proves that a declare does not write when its
// audit entry cannot be recorded (ADR 41, decision 5).
func TestDeclareRefusedWhenRecordFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()
	enableForced(t, f)

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
		t.Fatalf("first state: %v", err)
	}

	forced := newState("lin-old", 2, smallDoc)
	forced.Force = true

	errAudit := workspace.ErrConcurrentModification
	_, err := f.svc.BeginState(ctx, f.ws.ID, "alice", forced, authority(forcedOnly(), func(workspace.Override) error {
		return errAudit
	}))
	if !errors.Is(err, errAudit) {
		t.Fatalf("declare with a failing record: err = %v, want the record error", err)
	}

	if cur, curErr := f.svc.Current(ctx, f.ws.ID); curErr != nil || cur.Serial != 1 {
		t.Fatalf("current after a refused declare: serial %d, err %v, want the first state", cur.Serial, curErr)
	}
}

// effectiveOverrides returns the overrides that the unit allows: the namespace
// switch narrowed by the unit switch (ADR 44, decision 6).
func effectiveOverrides(t *testing.T, f fixture) (namespace.Overrides, error) {
	t.Helper()

	w, err := f.svc.GetByID(t.Context(), f.ws.ID)
	if err != nil {
		return namespace.Overrides{}, err
	}

	ns, err := f.namespaces.Get(t.Context(), w.Ref.Namespace)
	if err != nil {
		return namespace.Overrides{}, err
	}

	return ns.Overrides.And(w.Overrides), nil
}

// TestOverrideRefusalsSayTheWordsRoutesAndAction proves the refusal of a forced
// write and the refusal of an unlocked write keep their sentinel, say the plain
// words first, name both switch routes, and name the action after the words.
func TestOverrideRefusalsSayTheWordsRoutesAndAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(t *testing.T, f fixture) workspace.NewState
		wantErr  error
		wantWord string
		action   string
	}{
		{
			name: "forced write with the switch off and without the action",
			setup: func(t *testing.T, f fixture) workspace.NewState {
				t.Helper()

				if _, err := f.svc.Lock(t.Context(), f.ws.ID, "alice", "apply"); err != nil {
					t.Fatalf("lock: %v", err)
				}

				if err := f.push(t, newState("lin-old", 1, smallDoc), smallDoc); err != nil {
					t.Fatalf("first state: %v", err)
				}

				forced := newState("lin-old", 2, smallDoc)
				forced.Force = true

				return forced
			},
			wantErr:  workspace.ErrOverrideRefused,
			wantWord: "forced-write switch",
			action:   "unit:write-forced",
		},
		{
			name: "unlocked write with the switch off and without the action",
			setup: func(_ *testing.T, _ fixture) workspace.NewState {
				return newState("lin-old", 1, smallDoc)
			},
			wantErr:  workspace.ErrNotLocked,
			wantWord: "unlocked-write switch",
			action:   "unit:write-unlocked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			in := tt.setup(t, f)

			_, err := f.svc.BeginState(t.Context(), f.ws.ID, "alice", in, authority(noOverrides(), nil))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			checkRefusalText(t, err.Error(), tt.wantWord, tt.action)
		})
	}
}

// checkRefusalText fails the test unless msg says word, then action, and names
// both switch routes.
func checkRefusalText(t *testing.T, msg, word, action string) {
	t.Helper()

	w := strings.Index(msg, word)
	a := strings.Index(msg, action)

	if w < 0 || a < 0 || a < w {
		t.Fatalf("err = %q, want %q and then %q", msg, word, action)
	}

	for _, route := range []string{
		"PUT /api/server/namespaces/{namespace}/override-switches",
		"PUT /api/v2/workspaces/{id}/override-switches",
	} {
		if !strings.Contains(msg, route) {
			t.Errorf("err = %q, want route %q", msg, route)
		}
	}
}
