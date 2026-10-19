package storetest

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/gatetest"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// GateStore is every port that RunGate needs: the gate store, and the ports
// that make its namespaces, collections and units.
type GateStore interface {
	gate.Store
	namespace.Store
	collection.Store
	stack.Store
	workspace.Store
	workspace.DeletedStore
	audit.Store
	// BumpFailoverVersions adds 1 to the failover version of every namespace.
	BumpFailoverVersions(ctx context.Context) (int64, error)
}

// GateFactory returns a fresh, empty store for one subtest.
type GateFactory func(t *testing.T) GateStore

// RunGate executes the conformance cases of the gate store on stores from
// newStore: the rules of one scope for each kind of object, then the rules
// across scopes, the settings, the snapshot and the decision records.
func RunGate(t *testing.T, newStore GateFactory) {
	t.Helper()

	newHarness := func(t *testing.T) gatetest.Harness {
		t.Helper()

		s := newStore(t)

		return gatetest.Harness{Store: s, Domain: gateDomain{s: s}}
	}

	t.Run("objects", func(t *testing.T) {
		t.Parallel()

		t.Run("gate", func(t *testing.T) {
			t.Parallel()
			gatetest.RunObjects[gate.Gate](t, newHarness)
		})
		t.Run("access-limit", func(t *testing.T) {
			t.Parallel()
			gatetest.RunObjects[gate.AccessLimit](t, newHarness)
		})
		t.Run("approval-rule", func(t *testing.T) {
			t.Parallel()
			gatetest.RunObjects[gate.ApprovalRule](t, newHarness)
		})
		t.Run("ownership-rule", func(t *testing.T) {
			t.Parallel()
			gatetest.RunObjects[gate.OwnershipRule](t, newHarness)
		})
		t.Run("path-set", func(t *testing.T) {
			t.Parallel()
			gatetest.RunObjects[gate.PathSet](t, newHarness)
		})
	})

	t.Run("cases", func(t *testing.T) {
		t.Parallel()
		gatetest.RunStore(t, newHarness)
	})

	t.Run("every field of every kind is stored", func(t *testing.T) {
		t.Parallel()
		testGateFields(t, newStore(t))
	})

	t.Run("a purge removes the configuration of the unit", func(t *testing.T) {
		t.Parallel()
		testGatePurgeUnit(t, newStore(t))
	})

	t.Run("a delete of a collection removes its configuration", func(t *testing.T) {
		t.Parallel()
		testGateDeleteCollection(t, newStore(t))
	})
}

// putGateAt stores a gate and a path set at scope, with the fence of its
// namespace, and fails the test on an error.
func putGateAt(t *testing.T, s GateStore, scope gate.Scope, fence namespace.Fence) {
	t.Helper()

	g := gate.Gate{
		Scope: scope, ID: "cost-cap", Version: 0, DecisionPoint: gate.PointChangePlanned, Condition: "", Unless: "",
		Outcome: gate.OutcomeDeny, Message: "no", Mode: gate.ModeEvaluate, OnError: gate.OnErrorFail, Final: false,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:       "", Reads: []gate.ID{"network"},
	}
	e := gateEntry(fence.Namespace, "namespace:manage-gates")

	if _, err := s.Gates().Put(t.Context(), g, 0, fence, e); err != nil {
		t.Fatalf("Put(gate at %s) = %v", scope, err)
	}
}

// gateCount returns the count of gates at scope.
func gateCount(t *testing.T, s GateStore, scope gate.Scope) int {
	t.Helper()

	gates, err := s.Gates().List(t.Context(), scope)
	if err != nil {
		t.Fatalf("List(%s) = %v", scope, err)
	}

	return len(gates)
}

func testGatePurgeUnit(t *testing.T, s GateStore) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	if err = d.CreateUnit(t.Context(), "acme", "opentofu", "net", ""); err != nil {
		t.Fatalf("CreateUnit() = %v", err)
	}

	ns := gate.NamespaceScope("acme")
	unit, err := gate.UnitScope("acme", "opentofu", "net")
	if err != nil {
		t.Fatalf("UnitScope() = %v", err)
	}

	set := gate.PathSet{Scope: ns, ID: "network", Version: 0, Globs: []string{"modules/net/**"}}
	if _, err = s.PathSets().Put(t.Context(), set, 0, fence, gateEntry("acme", "namespace:manage-gates")); err != nil {
		t.Fatalf("Put(path set) = %v", err)
	}

	putGateAt(t, s, unit, fence)

	ref := workspace.Ref{Namespace: "acme", Name: "net"}

	w, err := s.GetWorkspace(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetWorkspace() = %v", err)
	}

	del := inventory.Deletion{Entry: gateEntry("acme", "workspace:delete"), PurgeAfter: gateTime()}
	if err = s.DeleteWorkspace(t.Context(), ref, fence, del); err != nil {
		t.Fatalf("DeleteWorkspace() = %v", err)
	}

	if n := gateCount(t, s, unit); n != 1 {
		t.Fatalf("gates of the deleted unit = %d, want 1: a soft delete keeps them", n)
	}

	purge := gateEntry("acme", "unit:purge")
	if err = s.PurgeWorkspace(t.Context(), w.ID, fence, time.Time{}, purge); err != nil {
		t.Fatalf("PurgeWorkspace() = %v", err)
	}

	if n := gateCount(t, s, unit); n != 0 {
		t.Fatalf("gates of the purged unit = %d, want 0", n)
	}

	// The path set that the gate read has no reader now, so a delete passes.
	err = s.PathSets().Delete(t.Context(), gate.ObjectRef{Scope: ns, ID: "network", Version: 1}, fence,
		gateEntry("acme", "namespace:manage-gates"))
	if err != nil {
		t.Fatalf("Delete(path set after the purge) = %v", err)
	}
}

func testGateDeleteCollection(t *testing.T, s GateStore) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	if err = d.CreateCollection(t.Context(), "acme", "prod"); err != nil {
		t.Fatalf("CreateCollection() = %v", err)
	}

	ns := gate.NamespaceScope("acme")
	prod := gate.CollectionScope("acme", "prod")

	set := gate.PathSet{Scope: ns, ID: "network", Version: 0, Globs: []string{"modules/net/**"}}
	if _, err = s.PathSets().Put(t.Context(), set, 0, fence, gateEntry("acme", "namespace:manage-gates")); err != nil {
		t.Fatalf("Put(path set) = %v", err)
	}

	putGateAt(t, s, prod, fence)

	ref := collection.Ref{Namespace: "acme", Name: "prod"}
	if err = s.DeleteCollection(t.Context(), ref, fence, gateEntry("acme", "collection:delete")); err != nil {
		t.Fatalf("DeleteCollection() = %v", err)
	}

	if err = d.CreateCollection(t.Context(), "acme", "prod"); err != nil {
		t.Fatalf("CreateCollection(again) = %v", err)
	}

	if n := gateCount(t, s, prod); n != 0 {
		t.Fatalf("gates of the collection created again = %d, want 0", n)
	}
}

// gateDomain makes the namespaces, the collections and the units, and reads
// the audit entries.
type gateDomain struct {
	s GateStore
}

var (
	_ gatetest.Domain    = gateDomain{s: nil}
	_ gatetest.Placement = gateDomain{s: nil}
	_ gatetest.Auditor   = gateDomain{s: nil}
)

// gateTime returns the time of the rows that the setup of the cases writes.
func gateTime() time.Time {
	return time.UnixMilli(1_700_000_000_000).UTC()
}

// CreateNamespace implements gatetest.Domain.
func (d gateDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: gateTime(), Name: name, HomeRegion: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := d.s.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, fmt.Errorf("create namespace %s: %w", name, err)
	}

	return ns.Fence(), nil
}

// BumpFence implements gatetest.Domain.
func (d gateDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	return namespace.Fence{Namespace: fence.Namespace, FailoverVersion: fence.FailoverVersion + 1}, nil
}

func gateEntry(ns namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: gateTime(), Namespace: ns, Subject: "admin", IDPSubject: "", TokenID: "", Action: action,
		Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
	}
}

// CreateCollection implements gatetest.Placement.
func (d gateDomain) CreateCollection(ctx context.Context, ns namespace.Name, name collection.Name) error {
	n, err := d.s.GetNamespace(ctx, ns)
	if err != nil {
		return fmt.Errorf("get namespace %s: %w", ns, err)
	}

	c := collection.Collection{
		CreatedAt: gateTime(), ID: ident.NewID("col-"), Ref: collection.Ref{Namespace: ns, Name: name},
	}

	if err = d.s.CreateCollection(ctx, c, n.Fence(), gateEntry(ns, "collection:create")); err != nil {
		return fmt.Errorf("create collection %s: %w", name, err)
	}

	return nil
}

// CreateUnit implements gatetest.Placement.
func (d gateDomain) CreateUnit(
	ctx context.Context, ns namespace.Name, tool, name string, in collection.Name,
) error {
	n, err := d.s.GetNamespace(ctx, ns)
	if err != nil {
		return fmt.Errorf("get namespace %s: %w", ns, err)
	}

	switch tool {
	case "pulumi":
		project, stackName, _ := strings.Cut(name, "/")

		ref, rerr := stack.ParseRef(ns.String(), project, stackName)
		if rerr != nil {
			return fmt.Errorf("parse stack reference: %w", rerr)
		}

		st := stack.Stack{
			CreatedAt: gateTime(), UpdatedAt: gateTime(), Tags: map[string]string{}, Ref: ref,
			ID: stack.ID(ident.NewID("stk-")), Version: 0,
		}
		if err = d.s.CreateStack(ctx, st, n.Fence()); err != nil {
			return fmt.Errorf("create stack %s: %w", name, err)
		}
	default:
		wname, perr := workspace.ParseName(name)
		if perr != nil {
			return fmt.Errorf("parse workspace name: %w", perr)
		}

		w := workspace.Workspace{
			CreatedAt: gateTime(),
			UpdatedAt: gateTime(),
			Lock:      workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			ID:        workspace.ID(ident.NewID("ws-")),
			Ref:       workspace.Ref{Namespace: ns, Name: wname},
			Overrides: namespace.Overrides{Forced: false, Unlocked: false},
			Revision:  1,
		}
		if err = d.s.CreateWorkspace(ctx, w, n.Fence()); err != nil {
			return fmt.Errorf("create workspace %s: %w", name, err)
		}
	}

	if in == "" {
		return nil
	}

	target, err := d.s.GetCollection(ctx, collection.Ref{Namespace: ns, Name: in})
	if err != nil {
		return fmt.Errorf("get collection %s: %w", in, err)
	}

	move := collection.Move{
		Unit: collection.UnitRef{Namespace: ns, Tool: tool, Name: name}, From: "",
		To: target.Ref, ToID: target.ID,
	}
	if err = d.s.SetUnitCollection(ctx, move, n.Fence(), gateEntry(ns, "unit:set-collection")); err != nil {
		return fmt.Errorf("move unit %s: %w", name, err)
	}

	return nil
}

// Entries implements gatetest.Auditor.
func (d gateDomain) Entries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error) {
	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := d.s.List(ctx, ns, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			return nil, fmt.Errorf("list audit entries of %s: %w", ns, err)
		}

		out = append(out, listing.Entries...)

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	// The listing is newest first, and the cases count the entries of the gate
	// store only: the setup writes of collections and units are in the same
	// chain, so drop them and return the rest oldest first.
	var kept []audit.Entry

	for i := range slices.Backward(out) {
		if strings.HasSuffix(out[i].Action, "manage-gates") {
			kept = append(kept, out[i])
		}
	}

	return kept, nil
}

// testGateFields stores one object of each kind with every field set, and
// checks that Get and Snapshot return the same fields.
func testGateFields(t *testing.T, s GateStore) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	scope := gate.NamespaceScope("acme")
	en := gateEntry("acme", "namespace:manage-gates")
	approvers := gate.Approvers{Groups: []string{"sre", "dba"}, Users: []string{"alice"}, UnitOwners: true}
	fresh := gate.FreshAuth{Set: true, Seconds: 300}

	set := gate.PathSet{Scope: scope, ID: "network", Version: 1, Globs: []string{"modules/net/**", "*.tf"}}
	rule := gate.ApprovalRule{
		Scope: scope, ID: "dba-review", Version: 1, Approvers: approvers, Count: 2, AuthorMayApprove: true,
		FreshAuth: fresh, Timeout: 36 * time.Hour,
	}
	limit := gate.AccessLimit{
		Scope: scope, ID: "no-agents", Version: 1, Policy: `forbid (principal, action, resource);`,
	}
	owner := gate.OwnershipRule{
		Scope:     scope,
		ID:        "net-owners",
		Version:   1,
		Mode:      gate.ModeEnforce,
		Paths:     []string{"modules/net/**"},
		When:      "context.plan.destroy > 0",
		Approvers: gate.Approvers{Groups: []string{"net"}, Users: nil, UnitOwners: false},
		Count:     1,
		FreshAuth: gate.NoFreshAuth(),
		Reads:     []gate.ID{"network"},
	}
	g := gate.Gate{
		Scope: scope, ID: "network-cap", Version: 1, DecisionPoint: gate.PointChangeApply, Condition: "context.a",
		Unless: "context.b", Outcome: gate.ApprovalOutcome("dba-review"), Message: "stop {{unit.tool}}",
		Mode: gate.ModeEnforce, OnError: gate.OnErrorSkip, Final: true,
		CheckedWith: gate.CheckedWith{
			Engine: "cedar/1", Schema: gate.SchemaVersion{Subject: string(gate.PointChangeApply), Major: 2, Minor: 1},
		},
		State: "", Reads: []gate.ID{"network"},
	}
	def := gate.ApprovalDefault{
		Scope: scope, Version: 1, Count: 3, FreshAuth: fresh, Approvers: approvers, Unless: "context.c",
		Reads: []gate.ID{"network"},
	}
	carry := gate.CarryOverSetting{Scope: scope, Version: 1, Value: gate.CarryOverForbidden}

	put := func(call string, err error) {
		t.Helper()

		if err != nil {
			t.Fatalf("%s = %v", call, err)
		}
	}

	_, err = s.PathSets().Put(t.Context(), set, 0, fence, en)
	put("Put(path set)", err)
	_, err = s.ApprovalRules().Put(t.Context(), rule, 0, fence, en)
	put("Put(approval rule)", err)
	_, err = s.AccessLimits().Put(t.Context(), limit, 0, fence, en)
	put("Put(access limit)", err)
	_, err = s.OwnershipRules().Put(t.Context(), owner, 0, fence, en)
	put("Put(ownership rule)", err)
	_, err = s.Gates().Put(t.Context(), g, 0, fence, en)
	put("Put(gate)", err)
	_, err = s.Settings().PutApprovalDefault(t.Context(), def, 0, fence, en)
	put("PutApprovalDefault()", err)
	_, err = s.Settings().PutCarryOver(t.Context(), carry, 0, fence, en)
	put("PutCarryOver()", err)

	cfg, err := s.Snapshot(t.Context(), []gate.Scope{scope})
	put("Snapshot()", err)

	got := gate.Config{
		Gates: cfg.Gates, AccessLimits: cfg.AccessLimits, ApprovalRules: cfg.ApprovalRules,
		OwnershipRules: cfg.OwnershipRules, PathSets: cfg.PathSets, ApprovalDefaults: cfg.ApprovalDefaults,
		CarryOver: cfg.CarryOver,
	}
	want := gate.Config{
		Gates: []gate.Gate{g}, AccessLimits: []gate.AccessLimit{limit}, ApprovalRules: []gate.ApprovalRule{rule},
		OwnershipRules: []gate.OwnershipRule{owner}, PathSets: []gate.PathSet{set},
		ApprovalDefaults: []gate.ApprovalDefault{def}, CarryOver: []gate.CarryOverSetting{carry},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot() = %+v, want %+v", got, want)
	}

	one, err := s.Gates().Get(t.Context(), scope, "network-cap")
	put("Get(gate)", err)

	if !reflect.DeepEqual(one, g) {
		t.Fatalf("Get(gate) = %+v, want %+v", one, g)
	}
}
