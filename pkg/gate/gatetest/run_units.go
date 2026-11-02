package gatetest

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Lifecycle is the optional part of a Domain that deletes, renames and moves
// the units that Placement makes. A case that needs it runs only with a
// Domain that implements Placement and Lifecycle, and is skipped otherwise.
type Lifecycle interface {
	// DeleteUnit deletes the unit name of tool in ns. The delete is not a
	// purge: the unit stays as a deleted unit, and its name is free.
	DeleteUnit(ctx context.Context, ns namespace.Name, tool, name string) error
	// RenameUnit gives the unit name of tool in ns the name next. The tool is
	// pulumi, and both names are <project>/<stack> of one project.
	RenameUnit(ctx context.Context, ns namespace.Name, tool, name, next string) error
	// MoveUnit moves the unit name of tool in ns from the collection that
	// holds it to the collection to.
	MoveUnit(ctx context.Context, ns namespace.Name, tool, name string, to collection.Name) error
}

// unitRunners returns the runners of the cases of the configuration of a
// unit.
func unitRunners() map[string]runner {
	return map[string]runner{
		"unit-reused-name-starts-clean":         eachKind(caseReusedNameClean),
		"unit-deleted-scope-not-found":          eachKind(caseDeletedScopeNotFound),
		"unit-rename-keeps-objects":             eachKind(caseRenameKeeps),
		"unit-move-keeps-objects":               eachKind(caseMoveKeeps),
		"unit-settings-reused-name-start-clean": once(caseSettingsReusedName),
		"snapshot-reused-name-starts-clean":     once(caseSnapshotReusedName),
	}
}

// lifecycle is the Lifecycle and the Placement of the Domain of one case.
type lifecycle struct {
	Lifecycle
	Placement
}

// lifecycle places the units and returns the Lifecycle and the Placement of
// the Domain. It skips the case when the Domain cannot place or change units.
func (e *env) lifecycle() lifecycle {
	e.t.Helper()

	l, ok := e.h.Domain.(Lifecycle)
	if !ok {
		e.t.Skip("the Domain does not implement Lifecycle")
	}

	p, ok := e.h.Domain.(Placement)
	if !ok {
		e.t.Skip("the Domain does not implement Placement")
	}

	e.place()

	return lifecycle{Lifecycle: l, Placement: p}
}

// unitOf returns the unit of a unit scope.
func unitOf(t *testing.T, scope gate.Scope) identity.Object {
	t.Helper()

	obj, ok := scope.Unit()
	if !ok {
		t.Fatalf("scope %s is not a unit scope", scope)
	}

	return obj
}

// deleteUnit deletes the unit of scope.
func (e *env) deleteUnit(l lifecycle, scope gate.Scope) {
	e.t.Helper()

	u := unitOf(e.t, scope)
	mustNil(e.t, "DeleteUnit("+scope.Key()+")", l.DeleteUnit(e.t.Context(), u.Namespace(), u.Tool(), u.UnitName()))
}

// recreateUnit makes a new unit with the tool and the name of scope, in no
// collection.
func (e *env) recreateUnit(l lifecycle, scope gate.Scope) {
	e.t.Helper()

	u := unitOf(e.t, scope)
	mustNil(e.t, "CreateUnit("+scope.Key()+")",
		l.CreateUnit(e.t.Context(), u.Namespace(), u.Tool(), u.UnitName(), ""))
}

// wantObjects fails the test when scope does not hold exactly the object a
// at version v, or, with v 0, holds any object.
func wantObjects(t *testing.T, e *env, k kindOps, scope gate.Scope, v int64) {
	t.Helper()

	got, err := k.list(e, scope)
	mustNil(t, "List("+scope.Key()+")", err)

	if v == 0 {
		if len(got) != 0 {
			t.Fatalf("List(%s) = %d objects, want none", scope, len(got))
		}

		_, err = k.get(e, scope, "a")
		wantErr(t, "Get("+scope.Key()+")", err, gate.ErrNotFound)

		return
	}

	if len(got) != 1 || got[0].ID != "a" || got[0].Version != v || got[0].Scope != scope {
		t.Fatalf("List(%s) = %v, want the object a at version %d of that scope", scope, got, v)
	}
}

func caseReusedNameClean(t *testing.T, e *env, k kindOps) {
	t.Helper()

	l := e.lifecycle()

	for _, scope := range []gate.Scope{e.fx.webProd, e.fx.acmeNet} {
		v, err := k.put(e, scope, "a", "one", 0)
		mustNil(t, "Put("+scope.Key()+")", err)

		_, err = k.put(e, scope, "a", "two", v)
		mustNil(t, "Put("+scope.Key()+", replace)", err)

		e.deleteUnit(l, scope)
		e.recreateUnit(l, scope)

		wantObjects(t, e, k, scope, 0)

		// The new unit starts clean, so the ID of the deleted unit's object
		// is free at its scope.
		v, err = k.put(e, scope, "a", "three", 0)
		mustNil(t, "Put("+scope.Key()+", new unit)", err)

		if v != 1 {
			t.Fatalf("Put(%s, new unit) = version %d, want 1", scope, v)
		}
	}
}

func caseDeletedScopeNotFound(t *testing.T, e *env, k kindOps) {
	t.Helper()

	l := e.lifecycle()

	_, err := k.put(e, e.fx.acmeNet, "a", "one", 0)
	mustNil(t, "Put(net)", err)

	e.deleteUnit(l, e.fx.acmeNet)

	before, _ := e.entries("acme")

	for _, scope := range []gate.Scope{e.fx.acmeNet, e.fx.ghost} {
		_, err = k.put(e, scope, "b", "one", 0)
		wantErr(t, "Put("+scope.Key()+")", err, gate.ErrNotFound)

		wantObjects(t, e, k, scope, 0)
	}

	e.wantEntries("acme", len(before))
}

func caseRenameKeeps(t *testing.T, e *env, k kindOps) {
	t.Helper()

	l := e.lifecycle()

	v, err := k.put(e, e.fx.webProd, "a", "one", 0)
	mustNil(t, "Put(web/prod)", err)

	mustNil(t, "RenameUnit(web/prod)", l.RenameUnit(t.Context(), "acme", "pulumi", "web/prod", "web/live"))

	wantObjects(t, e, k, e.fx.webLive, v)
	wantObjects(t, e, k, e.fx.webProd, 0)

	// A new unit that takes the old name starts clean, and the renamed unit
	// keeps its object.
	e.recreateUnit(l, e.fx.webProd)
	wantObjects(t, e, k, e.fx.webProd, 0)
	wantObjects(t, e, k, e.fx.webLive, v)
}

func caseMoveKeeps(t *testing.T, e *env, k kindOps) {
	t.Helper()

	l := e.lifecycle()

	v, err := k.put(e, e.fx.webProd, "a", "one", 0)
	mustNil(t, "Put(web/prod)", err)

	for _, to := range []collection.Name{"staging", "prod"} {
		mustNil(t, "MoveUnit(web/prod, "+string(to)+")", l.MoveUnit(t.Context(), "acme", "pulumi", "web/prod", to))
		wantObjects(t, e, k, e.fx.webProd, v)
	}

	for _, c := range []gate.Scope{e.fx.prod, e.fx.staging} {
		got, lerr := k.list(e, c)
		mustNil(t, "List("+c.Key()+")", lerr)

		if len(got) != 0 {
			t.Fatalf("List(%s) = %d objects, want none: the objects of a unit stay at its scope", c, len(got))
		}
	}
}

func caseSettingsReusedName(t *testing.T, e *env) {
	t.Helper()

	l := e.lifecycle()
	s := e.h.Store.Settings()

	e.populateSettings(e.fx.webProd)
	e.deleteUnit(l, e.fx.webProd)
	e.recreateUnit(l, e.fx.webProd)

	_, err := s.ApprovalDefault(t.Context(), e.fx.webProd)
	wantErr(t, "ApprovalDefault(new unit)", err, gate.ErrNotFound)

	got, err := s.CarryOver(t.Context(), e.fx.webProd)
	mustNil(t, "CarryOver(new unit)", err)

	if got.Value != gate.CarryOverOff || got.Version != 0 {
		t.Fatalf("CarryOver(new unit) = %+v, want off with version 0", got)
	}

	// The settings of the new unit are new: a put with prev 0 commits.
	if v := e.mustPutDefault(e.fx.webProd, 1); v != 1 {
		t.Fatalf("PutApprovalDefault(new unit) = version %d, want 1", v)
	}
}

func caseSnapshotReusedName(t *testing.T, e *env) {
	t.Helper()

	l := e.lifecycle()

	e.populate(e.fx.acme, "ns-")
	e.populate(e.fx.webProd, "unit-")
	e.populateSettings(e.fx.webProd)
	e.deleteUnit(l, e.fx.webProd)
	e.recreateUnit(l, e.fx.webProd)

	// The new unit is in no collection, so its chain has no collection.
	cfg, err := e.h.Store.Snapshot(t.Context(), []gate.Scope{e.fx.installation, e.fx.acme, e.fx.webProd})
	mustNil(t, "Snapshot(new unit)", err)

	for i := range cfg.Gates {
		if cfg.Gates[i].Scope == e.fx.webProd {
			t.Fatalf("Snapshot(new unit) holds the gate %s of the deleted unit", cfg.Gates[i].PolicyID())
		}
	}

	for i := range cfg.AccessLimits {
		if cfg.AccessLimits[i].Scope == e.fx.webProd {
			t.Fatalf("Snapshot(new unit) holds the access limit %s of the deleted unit", cfg.AccessLimits[i].ID)
		}
	}

	if len(cfg.Gates) != 2 || len(cfg.AccessLimits) != 2 || len(cfg.ApprovalRules) != 2 ||
		len(cfg.OwnershipRules) != 2 || len(cfg.PathSets) != 2 {
		t.Fatalf("Snapshot(new unit) = %+v, want the two objects of each kind of acme only", cfg)
	}

	if len(cfg.ApprovalDefaults) != 0 || len(cfg.CarryOver) != 0 {
		t.Fatalf("Snapshot(new unit) settings = %+v, %+v, want none", cfg.ApprovalDefaults, cfg.CarryOver)
	}
}
