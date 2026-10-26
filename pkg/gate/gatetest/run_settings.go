package gatetest

import (
	"fmt"
	"slices"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// settingsRunners returns the runners of the cases of gate.Settings and of
// the Snapshot of gate.Store.
func settingsRunners() map[string]runner {
	return map[string]runner{
		"approval-default-none":                   once(caseDefaultNone),
		"approval-default-round-trip":             once(caseDefaultRoundTrip),
		"approval-default-fields-must-be-valid":   once(caseDefaultFieldsValid),
		"approval-default-not-at-installation":    once(caseDefaultInstallation),
		"approval-default-path-sets-must-resolve": once(caseDefaultResolves),
		"approval-default-delete":                 once(caseDefaultDelete),
		"approval-default-delete-none":            once(caseDefaultDeleteNone),
		"approval-default-delete-stale":           once(caseDefaultDeleteStale),
		"approval-default-delete-at-installation": once(caseDefaultDeleteInstallation),
		"carry-over-default-off":                  once(caseCarryOverOff),
		"carry-over-round-trip":                   once(caseCarryOverRoundTrip),
		"carry-over-off-deletes":                  once(caseCarryOverDeletes),
		"carry-over-on-at-installation":           once(caseCarryOverInstallation),
		"settings-fence":                          once(caseSettingsFence),
		"snapshot-chain-in-order":                 once(caseSnapshotOrder),
		"snapshot-one-transaction":                once(caseSnapshotAtomic),
		"snapshot-zero-scope":                     once(caseSnapshotZero),
	}
}

// approvalDefault returns an approval default of scope.
func approvalDefault(scope gate.Scope, count int) gate.ApprovalDefault {
	return gate.ApprovalDefault{
		Scope:     scope,
		Version:   0,
		Count:     count,
		FreshAuth: gate.NoFreshAuth(),
		Approvers: gate.Approvers{
			Groups:     []string{"sre"},
			GroupIDs:   nil,
			Users:      nil,
			UnitOwners: false,
		},
		Unless: "",
		Reads:  nil,
	}
}

func (e *env) mustPutDefault(scope gate.Scope, count int) int64 {
	e.t.Helper()

	v, err := e.h.Store.Settings().PutApprovalDefault(e.t.Context(), approvalDefault(scope, count), 0, e.fence(scope),
		e.entry(scope))
	mustNil(e.t, "PutApprovalDefault("+scope.Key()+")", err)

	return v
}

func caseDefaultNone(t *testing.T, e *env) {
	t.Helper()

	_, err := e.h.Store.Settings().ApprovalDefault(t.Context(), e.fx.acme)
	wantErr(t, "ApprovalDefault(none)", err, gate.ErrNotFound)
}

func caseDefaultFieldsValid(t *testing.T, e *env) {
	t.Helper()

	tests := []struct {
		name   string
		mutate func(d *gate.ApprovalDefault)
	}{
		{"count zero", func(d *gate.ApprovalDefault) { d.Count = 0 }},
		{"count above ten", func(d *gate.ApprovalDefault) { d.Count = gate.MaxApprovalCount + 1 }},
		{"window below 60", func(d *gate.ApprovalDefault) { d.FreshAuth = gate.FreshAuth{Set: true, Seconds: 59} }},
		{"window above 900", func(d *gate.ApprovalDefault) { d.FreshAuth = gate.FreshAuth{Set: true, Seconds: 901} }},
		{"empty approver name", func(d *gate.ApprovalDefault) {
			d.Approvers = gate.Approvers{Groups: nil, GroupIDs: nil, Users: []string{""}, UnitOwners: false}
		}},
	}

	for _, tt := range tests {
		d := approvalDefault(e.fx.acme, 1)
		tt.mutate(&d)

		_, err := e.h.Store.Settings().PutApprovalDefault(t.Context(), d, 0, e.acme, entry("acme"))
		wantErr(t, "PutApprovalDefault("+tt.name+")", err, gate.ErrInvalid)
	}

	_, err := e.h.Store.Settings().ApprovalDefault(t.Context(), e.fx.acme)
	wantErr(t, "ApprovalDefault(after the refused puts)", err, gate.ErrNotFound)
	e.wantEntries("acme", 0)
}

func caseDefaultInstallation(t *testing.T, e *env) {
	t.Helper()

	_, err := e.h.Store.Settings().PutApprovalDefault(t.Context(), approvalDefault(e.fx.installation, 1), 0,
		zeroFence(), serverEntry())
	wantErr(t, "PutApprovalDefault(installation)", err, gate.ErrInvalid)
	e.wantEntries("@server", 0)
}

func caseDefaultDelete(t *testing.T, e *env) {
	t.Helper()

	v := e.mustPutDefault(e.fx.acme, 1)

	err := e.h.Store.Settings().DeleteApprovalDefault(t.Context(), e.fx.acme, v, e.acme, entry("acme"))
	mustNil(t, "DeleteApprovalDefault()", err)

	_, err = e.h.Store.Settings().ApprovalDefault(t.Context(), e.fx.acme)
	wantErr(t, "ApprovalDefault(after delete)", err, gate.ErrNotFound)
	e.wantEntries("acme", 2)

	v, err = e.h.Store.Settings().
		PutApprovalDefault(t.Context(), approvalDefault(e.fx.acme, 2), 0, e.acme, entry("acme"))
	mustNil(t, "PutApprovalDefault(after delete)", err)

	if v != 1 {
		t.Fatalf("PutApprovalDefault(after delete) = version %d, want 1", v)
	}
}

func caseDefaultDeleteNone(t *testing.T, e *env) {
	t.Helper()

	err := e.h.Store.Settings().DeleteApprovalDefault(t.Context(), e.fx.acme, 1, e.acme, entry("acme"))
	wantErr(t, "DeleteApprovalDefault(none)", err, gate.ErrNotFound)
}

func caseDefaultDeleteStale(t *testing.T, e *env) {
	t.Helper()

	v := e.mustPutDefault(e.fx.acme, 1)

	err := e.h.Store.Settings().DeleteApprovalDefault(t.Context(), e.fx.acme, v+1, e.acme, entry("acme"))
	wantErr(t, "DeleteApprovalDefault(stale)", err, gate.ErrConflict)

	if _, gerr := e.h.Store.Settings().ApprovalDefault(t.Context(), e.fx.acme); gerr != nil {
		t.Fatalf("ApprovalDefault(after the refused delete) = %v", gerr)
	}

	e.wantEntries("acme", 1)
}

func caseDefaultDeleteInstallation(t *testing.T, e *env) {
	t.Helper()

	err := e.h.Store.Settings().DeleteApprovalDefault(t.Context(), e.fx.installation, 1, zeroFence(), serverEntry())
	wantErr(t, "DeleteApprovalDefault(installation)", err, gate.ErrInvalid)
}

func caseCarryOverOff(t *testing.T, e *env) {
	t.Helper()

	for _, scope := range []gate.Scope{e.fx.installation, e.fx.acme} {
		got, err := e.h.Store.Settings().CarryOver(t.Context(), scope)
		mustNil(t, "CarryOver("+scope.Key()+")", err)

		if got.Value != gate.CarryOverOff || got.Version != 0 {
			t.Fatalf("CarryOver(%s) = %+v, want off with version 0", scope, got)
		}
	}
}

func carryOver(scope gate.Scope, v gate.CarryOver) gate.CarryOverSetting {
	return gate.CarryOverSetting{Scope: scope, Version: 0, Value: v}
}

func caseCarryOverRoundTrip(t *testing.T, e *env) {
	t.Helper()

	s := e.h.Store.Settings()

	for _, scope := range []gate.Scope{e.fx.acme, e.fx.prod, e.fx.webProd} {
		fence, en := e.fence(scope), e.entry(scope)

		v, err := s.PutCarryOver(t.Context(), carryOver(scope, gate.CarryOverOn), 0, fence, en)
		mustNil(t, "PutCarryOver(on)", err)

		if v != 1 {
			t.Fatalf("PutCarryOver(new) = version %d, want 1", v)
		}

		_, err = s.PutCarryOver(t.Context(), carryOver(scope, gate.CarryOverOn), 0, fence, en)
		wantErr(t, "PutCarryOver(prev 0, exists)", err, gate.ErrConflict)

		_, err = s.PutCarryOver(t.Context(), carryOver(scope, gate.CarryOverForbidden), 7, fence, en)
		wantErr(t, "PutCarryOver(stale prev)", err, gate.ErrConflict)

		next, err := s.PutCarryOver(t.Context(), carryOver(scope, gate.CarryOverForbidden), v, fence, en)
		mustNil(t, "PutCarryOver(forbidden)", err)

		got, err := s.CarryOver(t.Context(), scope)
		mustNil(t, "CarryOver("+scope.Key()+")", err)

		if got.Value != gate.CarryOverForbidden || got.Version != next || next != v+1 {
			t.Fatalf("CarryOver(%s) = %+v after version %d, want forbidden at %d", scope, got, next, v+1)
		}
	}

	_, err := s.PutCarryOver(t.Context(), carryOver(e.fx.globex, gate.CarryOverOn), 2, e.globex, entry("globex"))
	wantErr(t, "PutCarryOver(prev above 0, none)", err, gate.ErrNotFound)

	_, err = s.PutCarryOver(t.Context(), carryOver(e.fx.globex, "sometimes"), 0, e.globex, entry("globex"))
	wantErr(t, "PutCarryOver(unknown value)", err, gate.ErrInvalid)
}

func caseCarryOverDeletes(t *testing.T, e *env) {
	t.Helper()

	s := e.h.Store.Settings()

	v, err := s.PutCarryOver(t.Context(), carryOver(e.fx.acme, gate.CarryOverOn), 0, e.acme, entry("acme"))
	mustNil(t, "PutCarryOver(on)", err)

	_, err = s.PutCarryOver(t.Context(), carryOver(e.fx.acme, gate.CarryOverOff), v+1, e.acme, entry("acme"))
	wantErr(t, "PutCarryOver(off, stale)", err, gate.ErrConflict)

	off, err := s.PutCarryOver(t.Context(), carryOver(e.fx.acme, gate.CarryOverOff), v, e.acme, entry("acme"))
	mustNil(t, "PutCarryOver(off)", err)

	if off != 0 {
		t.Fatalf("PutCarryOver(off) = version %d, want 0", off)
	}

	got, err := s.CarryOver(t.Context(), e.fx.acme)
	mustNil(t, "CarryOver(after off)", err)

	if got.Value != gate.CarryOverOff || got.Version != 0 {
		t.Fatalf("CarryOver(after off) = %+v, want off with version 0", got)
	}

	next, err := s.PutCarryOver(t.Context(), carryOver(e.fx.acme, gate.CarryOverOn), 0, e.acme, entry("acme"))
	mustNil(t, "PutCarryOver(on after off)", err)

	if next != 1 {
		t.Fatalf("PutCarryOver(on after off) = version %d, want 1", next)
	}
}

func caseCarryOverInstallation(t *testing.T, e *env) {
	t.Helper()

	s := e.h.Store.Settings()

	_, err := s.PutCarryOver(t.Context(), carryOver(e.fx.installation, gate.CarryOverOn), 0, zeroFence(), serverEntry())
	wantErr(t, "PutCarryOver(on, installation)", err, gate.ErrInvalid)

	v, err := s.PutCarryOver(t.Context(), carryOver(e.fx.installation, gate.CarryOverForbidden), 0, zeroFence(),
		serverEntry())
	mustNil(t, "PutCarryOver(forbidden, installation)", err)

	if v != 1 {
		t.Fatalf("PutCarryOver(forbidden, installation) = version %d, want 1", v)
	}
}

func caseSettingsFence(t *testing.T, e *env) {
	t.Helper()

	stale := e.acme

	_, err := e.h.Domain.BumpFence(t.Context(), e.acme)
	mustNil(t, "BumpFence()", err)

	s := e.h.Store.Settings()

	_, err = s.PutCarryOver(t.Context(), carryOver(e.fx.prod, gate.CarryOverOn), 0, stale, entry("acme"))
	wantErr(t, "PutCarryOver(stale fence)", err, namespace.ErrFenced)

	_, err = s.PutApprovalDefault(t.Context(), approvalDefault(e.fx.prod, 1), 0, stale, entry("acme"))
	wantErr(t, "PutApprovalDefault(stale fence)", err, namespace.ErrFenced)

	got, err := s.CarryOver(t.Context(), e.fx.prod)
	mustNil(t, "CarryOver()", err)

	if got.Version != 0 {
		t.Fatalf("CarryOver() after the refused write = %+v, want none", got)
	}

	e.wantEntries("acme", 0)
}

func caseSnapshotZero(t *testing.T, e *env) {
	t.Helper()

	_, err := e.h.Store.Snapshot(t.Context(), []gate.Scope{e.fx.acme, {}})
	wantErr(t, "Snapshot(zero scope)", err, gate.ErrInvalid)

	cfg, err := e.h.Store.Snapshot(t.Context(), nil)
	mustNil(t, "Snapshot(no scope)", err)

	if len(cfg.Gates) != 0 || len(cfg.PathSets) != 0 {
		t.Fatalf("Snapshot(no scope) = %+v, want an empty configuration", cfg)
	}
}

// populate stores one object of each kind at scope, with IDs that start with
// prefix, in reverse order of the ID.
func (e *env) populate(scope gate.Scope, prefix string) {
	e.t.Helper()

	for _, suffix := range []string{"b", "a"} {
		id := gate.ID(prefix + suffix)
		e.mustPutGate(e.gate(scope, id, nil), 0)

		l := sample[gate.AccessLimit](e.t, scope, id, "one")
		_, err := e.h.Store.AccessLimits().Put(e.t.Context(), l, 0, e.fence(scope), e.entry(scope))
		mustNil(e.t, "Put(access limit)", err)

		e.mustPutRule(scope, id)
		e.mustPutPathSet(scope, id)

		o := sample[gate.OwnershipRule](e.t, scope, id, "one")
		_, err = e.h.Store.OwnershipRules().Put(e.t.Context(), o, 0, e.fence(scope), e.entry(scope))
		mustNil(e.t, "Put(ownership rule)", err)
	}
}

// populateSettings stores the approval default, if the scope holds one, and
// the carry-over setting of scope.
func (e *env) populateSettings(scope gate.Scope) {
	e.t.Helper()

	if scope.HoldsApprovalDefault() {
		e.mustPutDefault(scope, 2)
	}

	_, err := e.h.Store.Settings().PutCarryOver(e.t.Context(), carryOver(scope, gate.CarryOverForbidden), 0,
		e.fence(scope), e.entry(scope))
	mustNil(e.t, "PutCarryOver()", err)
}

func caseSnapshotOrder(t *testing.T, e *env) {
	t.Helper()

	chain := []gate.Scope{e.fx.installation, e.fx.acme, e.fx.prod, e.fx.webProd}
	prefixes := []string{"i", "n", "c", "u"}

	other := []gate.Scope{e.fx.globex, e.fx.staging}

	for i, scope := range append(slices.Clone(chain), other...) {
		prefix := "x"
		if i < len(prefixes) {
			prefix = prefixes[i]
		}

		e.populate(scope, prefix)
		e.populateSettings(scope)
	}

	cfg, err := e.h.Store.Snapshot(t.Context(), chain)
	mustNil(t, "Snapshot()", err)

	wantIDs := []string{"ia", "ib", "na", "nb", "ca", "cb", "ua", "ub"}

	check := func(kind string, got []string) {
		t.Helper()

		if !slices.Equal(got, wantIDs) {
			t.Fatalf("Snapshot().%s = %v, want %v", kind, got, wantIDs)
		}
	}

	check("Gates", mapRefs(cfg.Gates, gate.Gate.Ref))
	check("AccessLimits", mapRefs(cfg.AccessLimits, func(l gate.AccessLimit) gate.ObjectRef { return l.Ref() }))
	check("ApprovalRules", mapRefs(cfg.ApprovalRules, gate.ApprovalRule.Ref))
	check("OwnershipRules", mapRefs(cfg.OwnershipRules, gate.OwnershipRule.Ref))
	check("PathSets", mapRefs(cfg.PathSets, gate.PathSet.Ref))

	defaults := make([]string, len(cfg.ApprovalDefaults))
	for i := range cfg.ApprovalDefaults {
		defaults[i] = cfg.ApprovalDefaults[i].Scope.Key()
	}

	carry := make([]string, len(cfg.CarryOver))
	for i := range cfg.CarryOver {
		carry[i] = cfg.CarryOver[i].Scope.Key()
	}

	wantDefaults := []string{e.fx.acme.Key(), e.fx.prod.Key(), e.fx.webProd.Key()}
	if !slices.Equal(defaults, wantDefaults) {
		t.Fatalf("Snapshot().ApprovalDefaults = %v, want %v", defaults, wantDefaults)
	}

	wantCarry := []string{e.fx.installation.Key(), e.fx.acme.Key(), e.fx.prod.Key(), e.fx.webProd.Key()}
	if !slices.Equal(carry, wantCarry) {
		t.Fatalf("Snapshot().CarryOver = %v, want %v", carry, wantCarry)
	}

	// A kind is in the order of the scopes first.
	for i := range cfg.Gates {
		if cfg.Gates[i].Scope != chain[i/2] {
			t.Fatalf("Snapshot().Gates[%d] is in %s, want %s", i, cfg.Gates[i].Scope, chain[i/2])
		}
	}
}

// mapRefs returns the IDs of the objects, and fails on none.
func mapRefs[T any](objs []T, ref func(T) gate.ObjectRef) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = string(ref(o).ID)
	}

	return out
}

func caseSnapshotAtomic(t *testing.T, e *env) {
	t.Helper()

	scopes := []gate.Scope{e.fx.acme, e.fx.webProd}
	versions := make(map[gate.Scope]int64, len(scopes))

	for _, scope := range scopes {
		versions[scope] = e.mustPutGate(saltedGate(e, scope, "0"), 0)
	}

	// Each write replaces a gate of one scope in one transaction. The
	// condition and the message of a gate hold the salt of the write, so a
	// snapshot that mixed two versions of one gate would show two salts.
	write := func() error {
		for i := 1; i <= snapshotWrites; i++ {
			for _, scope := range scopes {
				next, err := e.putGate(saltedGate(e, scope, strconv.Itoa(i)), versions[scope])
				if err != nil {
					return err
				}

				versions[scope] = next
			}
		}

		return nil
	}
	read := func() error { return readSnapshots(t, e, scopes) }

	for i, err := range concurrently(read, write, read) {
		mustNil(t, fmt.Sprintf("snapshot round, worker %d", i), err)
	}
}

// snapshotWrites is the count of writes of each scope in caseSnapshotAtomic.
const snapshotWrites = 25

// saltedGate returns the gate "cap" of scope with the salt in its condition
// and in its message.
func saltedGate(e *env, scope gate.Scope, salt string) gate.Gate {
	return e.gate(scope, "cap", func(g *gate.Gate) {
		g.Condition = "context.x == " + salt
		g.Message = salt
	})
}

// readSnapshots takes snapshots of scopes and checks that each gate holds one
// salt.
func readSnapshots(t *testing.T, e *env, scopes []gate.Scope) error {
	t.Helper()

	for range snapshotWrites {
		cfg, err := e.h.Store.Snapshot(t.Context(), scopes)
		if err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}

		if len(cfg.Gates) != len(scopes) {
			return fmt.Errorf("%w: snapshot holds %d gates, want %d", gate.ErrNotFound, len(cfg.Gates), len(scopes))
		}

		for i := range cfg.Gates {
			g := &cfg.Gates[i]
			if want := "context.x == " + g.Message; g.Condition != want {
				return fmt.Errorf("%w: gate %s has the condition %q and the message %q", gate.ErrInvalid, g.PolicyID(),
					g.Condition, g.Message)
			}
		}
	}

	return nil
}
