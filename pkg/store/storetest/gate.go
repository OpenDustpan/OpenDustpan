package storetest

import (
	"context"
	"errors"
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
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
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

	t.Run("approvers", func(t *testing.T) {
		t.Parallel()
		gatetest.RunApprovers(t, kindHarness(newStore, installation.KindProduction),
			kindHarness(newStore, installation.KindDev))
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

	t.Run("a purge of a deleted unit keeps the configuration of a new unit with its name", func(t *testing.T) {
		t.Parallel()

		for _, tc := range gateUnitCases() {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				testGatePurgeAfterReuse(t, newStore(t), tc)
			})
		}
	})

	t.Run("a deleted unit keeps its collection and labels for the gates", func(t *testing.T) {
		t.Parallel()

		for _, tc := range gateUnitCases() {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				testGateDeletedUnitFacts(t, newStore(t), tc)
			})
		}
	})

	t.Run("a rename of a stack keeps its configuration", func(t *testing.T) {
		t.Parallel()
		testGateRenameStack(t, newStore(t))
	})

	t.Run("a rename to the name of a deleted unit keeps the configuration of each unit", func(t *testing.T) {
		t.Parallel()
		testGateRenameToUsedName(t, newStore(t))
	})

	t.Run("a restore under a new name keeps its configuration", func(t *testing.T) {
		t.Parallel()

		for _, tc := range gateUnitCases() {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				testGateRestoreNewName(t, newStore(t), tc)
			})
		}
	})

	t.Run("a restore under a new name keeps the configuration of a live unit with the old name", func(t *testing.T) {
		t.Parallel()

		for _, tc := range gateUnitCases() {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				testGateRestoreAfterReuse(t, newStore(t), tc)
			})
		}
	})

	t.Run("a purge with a full entry ends its changes with the count left out", func(t *testing.T) {
		t.Parallel()
		testGatePurgeChangeCap(t, newStore(t))
	})

	t.Run("a unit with a gate cannot join a collection whose final gate has its ID", func(t *testing.T) {
		t.Parallel()

		for _, tc := range gateUnitCases() {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				testGateJoinFinal(t, newStore(t), tc)
			})
		}
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

	// The rows of a deleted unit stay until the purge, but no name reaches
	// them: a deleted unit holds no name.
	if n := gateCount(t, s, unit); n != 0 {
		t.Fatalf("gates of the name of the deleted unit = %d, want 0", n)
	}

	err = s.PathSets().Delete(t.Context(), gate.ObjectRef{Scope: ns, ID: "network", Version: 1}, fence,
		gateEntry("acme", "namespace:manage-gates"))
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Delete(path set that the deleted unit's gate reads) = %v, want gate.ErrInvalid", err)
	}

	purge := gateEntry("acme", "unit:purge")
	if err = s.PurgeWorkspace(t.Context(), w.ID, fence, time.Time{}, purge); err != nil {
		t.Fatalf("PurgeWorkspace() = %v", err)
	}

	if n := gateCount(t, s, unit); n != 0 {
		t.Fatalf("gates of the purged unit = %d, want 0", n)
	}

	wantRemoved(t, s, "unit:purge", []string{"removed:gate:cost-cap"})

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

	wantRemoved(t, s, "collection:delete", []string{"removed:gate:cost-cap"})

	if err = d.CreateCollection(t.Context(), "acme", "prod"); err != nil {
		t.Fatalf("CreateCollection(again) = %v", err)
	}

	if n := gateCount(t, s, prod); n != 0 {
		t.Fatalf("gates of the collection created again = %d, want 0", n)
	}
}

// kindHarness returns a factory of harnesses over stores of the installation
// kind want. It claims the kind first, and skips the case when the store
// cannot claim or read a kind, or holds another one: the check of the
// approvers reads the kind of the database through ReadKind.
func kindHarness(newStore GateFactory, want installation.Kind) gatetest.Factory {
	return func(t *testing.T) gatetest.Harness {
		t.Helper()

		s := newStore(t)

		claimer, ok := any(s).(installation.KindStore)
		reader, readable := any(s).(interface {
			ReadKind(ctx context.Context) (installation.Kind, error)
		})

		if want == installation.KindProduction && !ok || !readable {
			// A store that reads no kind counts as production: the check
			// fails closed.
			return gatetest.Harness{Store: s, Domain: gateDomain{s: s}}
		}

		if !ok || !readable {
			t.Skip("the store cannot claim and read an installation kind")
		}

		if _, err := claimer.ClaimKind(t.Context(), want); err != nil {
			t.Fatalf("ClaimKind(%s) = %v", want, err)
		}

		if got, err := reader.ReadKind(t.Context()); err != nil || got != want {
			t.Skipf("the store holds the kind %q (%v), the case needs %q", got, err, want)
		}

		return gatetest.Harness{Store: s, Domain: gateDomain{s: s}}
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
	_ gatetest.Lifecycle = gateDomain{s: nil}
	_ gatetest.Auditor   = gateDomain{s: nil}
)

// gateTime returns the time of the rows that the setup of the cases writes.
func gateTime() time.Time {
	return time.UnixMilli(1_700_000_000_000).UTC()
}

// CreateNamespace implements gatetest.Domain.
func (d gateDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: gateTime(), Name: name, PrimaryInstallation: "eu-west", FailoverVersion: 1,
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
		Reason: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "",
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
			NamespaceID: "",
			CreatedAt:   gateTime(), UpdatedAt: gateTime(), Tags: map[string]string{}, Ref: ref,
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
			NamespaceID: "",
			CreatedAt:   gateTime(),
			UpdatedAt:   gateTime(),
			Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			LastRelease: workspace.NoRelease(),
			ID:          workspace.ID(ident.NewID("ws-")),
			Ref:         workspace.Ref{Namespace: ns, Name: wname},
			Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
			Revision:    1, Origin: identity.StateOrigin{Job: "", Commit: ""},
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
		To: target.Ref, ToID: target.ID, EndsLock: false,
	}
	if err = d.s.SetUnitCollection(ctx, move, n.Fence(), gateEntry(ns, "unit:set-collection")); err != nil {
		return fmt.Errorf("move unit %s: %w", name, err)
	}

	return nil
}

// DeleteUnit implements gatetest.Lifecycle.
func (d gateDomain) DeleteUnit(ctx context.Context, ns namespace.Name, tool, name string) (string, error) {
	n, err := d.s.GetNamespace(ctx, ns)
	if err != nil {
		return "", fmt.Errorf("get namespace %s: %w", ns, err)
	}

	del := inventory.Deletion{Entry: gateEntry(ns, "unit:delete"), PurgeAfter: gateTime()}

	if tool == "pulumi" {
		project, stackName, _ := strings.Cut(name, "/")

		ref, rerr := stack.ParseRef(ns.String(), project, stackName)
		if rerr != nil {
			return "", fmt.Errorf("parse stack reference: %w", rerr)
		}

		st, gerr := d.s.GetStack(ctx, ref)
		if gerr != nil {
			return "", fmt.Errorf("get stack %s: %w", name, gerr)
		}

		if err = d.s.ForceDeleteStack(ctx, ref, n.Fence(), del); err != nil {
			return "", fmt.Errorf("delete stack %s: %w", name, err)
		}

		return st.ID.String(), nil
	}

	ref := workspace.Ref{Namespace: ns, Name: workspace.Name(name)}

	w, err := d.s.GetWorkspace(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("get workspace %s: %w", name, err)
	}

	if err = d.s.DeleteWorkspace(ctx, ref, n.Fence(), del); err != nil {
		return "", fmt.Errorf("delete workspace %s: %w", name, err)
	}

	return w.ID.String(), nil
}

// RenameUnit implements gatetest.Lifecycle. Only a Pulumi stack has a rename.
func (d gateDomain) RenameUnit(ctx context.Context, ns namespace.Name, tool, name, next string) error {
	if tool != "pulumi" {
		return fmt.Errorf("rename of a unit of the tool %s: %w", tool, errors.ErrUnsupported)
	}

	n, err := d.s.GetNamespace(ctx, ns)
	if err != nil {
		return fmt.Errorf("get namespace %s: %w", ns, err)
	}

	project, stackName, _ := strings.Cut(name, "/")

	ref, err := stack.ParseRef(ns.String(), project, stackName)
	if err != nil {
		return fmt.Errorf("parse stack reference: %w", err)
	}

	_, nextStack, _ := strings.Cut(next, "/")

	newName, err := stack.ParseName(nextStack)
	if err != nil {
		return fmt.Errorf("parse stack name: %w", err)
	}

	if err = d.s.RenameStack(ctx, ref, newName, n.Fence(), renameEntry(ns, ref)); err != nil {
		return fmt.Errorf("rename stack %s: %w", name, err)
	}

	return nil
}

// MoveUnit implements gatetest.Lifecycle.
func (d gateDomain) MoveUnit(ctx context.Context, ns namespace.Name, tool, name string, to collection.Name) error {
	n, err := d.s.GetNamespace(ctx, ns)
	if err != nil {
		return fmt.Errorf("get namespace %s: %w", ns, err)
	}

	unit := collection.UnitRef{Namespace: ns, Tool: tool, Name: name}

	from, err := d.s.UnitCollection(ctx, unit)
	if err != nil && !errors.Is(err, collection.ErrNoCollection) {
		return fmt.Errorf("get the collection of %s: %w", name, err)
	}

	target, err := d.s.GetCollection(ctx, collection.Ref{Namespace: ns, Name: to})
	if err != nil {
		return fmt.Errorf("get collection %s: %w", to, err)
	}

	move := collection.Move{Unit: unit, From: from.Name, To: target.Ref, ToID: target.ID, EndsLock: false}
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
	approvers := gate.Approvers{
		Groups: []string{
			"sre",
			"dba",
		},
		GroupIDs:   []string{"id-sre", "id-dba"},
		Users:      []string{"alice"},
		UnitOwners: true,
	}
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
		Approvers: gate.Approvers{Groups: []string{"net"}, GroupIDs: []string{"id-net"}, Users: nil, UnitOwners: false},
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

// wantRemoved checks that the audit entry of the namespace acme with the action names exactly
// the changes keys: the removed configuration objects.
func wantRemoved(t *testing.T, s GateStore, action string, keys []string) {
	t.Helper()

	listing, err := s.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("List(audit) = %v", err)
	}

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Action != action {
			continue
		}

		got := make([]string, len(e.Changes))
		for j, c := range e.Changes {
			got[j] = c.Key
		}

		slices.Sort(got)

		if !slices.Equal(got, keys) {
			t.Fatalf("changes of the %s entry = %v, want %v", action, got, keys)
		}

		return
	}

	t.Fatalf("no audit entry with the action %s", action)
}

// gateUnitCase is one tool, and the name of one of its units.
type gateUnitCase struct {
	name string
	tool string
	unit string
}

func gateUnitCases() []gateUnitCase {
	return []gateUnitCase{
		{name: "an OpenTofu workspace", tool: "opentofu", unit: "net"},
		{name: "a Pulumi stack", tool: "pulumi", unit: "web/prod"},
	}
}

// testGateDeletedUnitFacts puts the unit of tc in a collection, deletes it and
// reads its facts: the real collection, and for a Pulumi stack its tags as
// labels. A live unit, an unknown unit and a purged unit are not found.
func testGateDeletedUnitFacts(t *testing.T, s GateStore, tc gateUnitCase) {
	t.Helper()

	facts, ok := s.(inventory.DeletedFactsReader)
	if !ok {
		t.Fatalf("store %T lacks the deleted facts reader", s)
	}

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	if err = d.CreateCollection(t.Context(), "acme", "payments"); err != nil {
		t.Fatalf("CreateCollection() = %v", err)
	}

	if err = d.CreateUnit(t.Context(), "acme", tc.tool, tc.unit, "payments"); err != nil {
		t.Fatalf("CreateUnit() = %v", err)
	}

	wantLabels := tagGateUnit(t, s, tc, fence)
	id := softDeleteUnitID(t, s, tc, fence)

	got, err := facts.GetDeletedUnitFacts(t.Context(), "acme", tc.tool, id)
	if err != nil || got.Collection != "payments" || len(got.Labels) != wantLabels {
		t.Fatalf("GetDeletedUnitFacts() = %+v, %v, want collection payments and %d labels", got, err, wantLabels)
	}

	wantFactsNotFound(t, facts, tc.tool, "unknown-id")
	purgeGateUnit(t, s, tc, id, fence)
	wantFactsNotFound(t, facts, tc.tool, id)
}

// tagGateUnit gives a Pulumi stack one tag and returns the number of labels
// that the unit then has. An OpenTofu workspace has none.
func tagGateUnit(t *testing.T, s GateStore, tc gateUnitCase, fence namespace.Fence) int {
	t.Helper()

	if tc.tool != "pulumi" {
		return 0
	}

	project, name, _ := strings.Cut(tc.unit, "/")

	ref, err := stack.ParseRef("acme", project, name)
	if err != nil {
		t.Fatalf("ParseRef() = %v", err)
	}

	if err = s.UpdateStackTags(t.Context(), ref, map[string]string{"team": "core"}, gateTime(), fence); err != nil {
		t.Fatalf("UpdateStackTags() = %v", err)
	}

	return 1
}

// purgeGateUnit purges the deleted unit id of tc.
func purgeGateUnit(t *testing.T, s GateStore, tc gateUnitCase, id string, fence namespace.Fence) {
	t.Helper()

	purge := gateEntry("acme", "unit:purge")

	var err error
	if tc.tool == "pulumi" {
		err = s.PurgeStack(t.Context(), stack.ID(id), fence, time.Time{}, purge)
	} else {
		err = s.PurgeWorkspace(t.Context(), workspace.ID(id), fence, time.Time{}, purge)
	}

	if err != nil {
		t.Fatalf("purge = %v", err)
	}
}

// wantFactsNotFound expects ErrNotFound from the facts of the deleted unit id.
func wantFactsNotFound(t *testing.T, facts inventory.DeletedFactsReader, tool, id string) {
	t.Helper()

	if _, err := facts.GetDeletedUnitFacts(t.Context(), "acme", tool, id); !errors.Is(err, inventory.ErrNotFound) {
		t.Fatalf("GetDeletedUnitFacts(%s) = %v, want ErrNotFound", id, err)
	}
}

// softDeleteUnit soft-deletes the unit of tc and returns the function that
// purges it, as the unit was before the delete.
func softDeleteUnit(t *testing.T, s GateStore, tc gateUnitCase, fence namespace.Fence) func() error {
	t.Helper()

	id := softDeleteUnitID(t, s, tc, fence)
	purge := gateEntry(fence.Namespace, "unit:purge")

	if tc.tool == "pulumi" {
		return func() error { return s.PurgeStack(t.Context(), stack.ID(id), fence, time.Time{}, purge) }
	}

	return func() error { return s.PurgeWorkspace(t.Context(), workspace.ID(id), fence, time.Time{}, purge) }
}

// softDeleteUnitID soft-deletes the unit of tc and returns its ID.
func softDeleteUnitID(t *testing.T, s GateStore, tc gateUnitCase, fence namespace.Fence) string {
	t.Helper()

	ns := fence.Namespace
	del := inventory.Deletion{Entry: gateEntry(ns, "unit:delete"), PurgeAfter: gateTime()}

	if tc.tool == "pulumi" {
		project, name, _ := strings.Cut(tc.unit, "/")

		ref, err := stack.ParseRef(ns.String(), project, name)
		if err != nil {
			t.Fatalf("ParseRef() = %v", err)
		}

		st, err := s.GetStack(t.Context(), ref)
		if err != nil {
			t.Fatalf("GetStack() = %v", err)
		}

		if err = s.ForceDeleteStack(t.Context(), ref, fence, del); err != nil {
			t.Fatalf("ForceDeleteStack() = %v", err)
		}

		return st.ID.String()
	}

	ref := workspace.Ref{Namespace: ns, Name: workspace.Name(tc.unit)}

	w, err := s.GetWorkspace(t.Context(), ref)
	if err != nil {
		t.Fatalf("GetWorkspace() = %v", err)
	}

	if err = s.DeleteWorkspace(t.Context(), ref, fence, del); err != nil {
		t.Fatalf("DeleteWorkspace() = %v", err)
	}

	return w.ID.String()
}

// deletedUnit is a soft-deleted unit of the tool of tc.
type deletedUnit struct {
	tc    gateUnitCase
	fence namespace.Fence
	id    string
}

// restoreAs restores the deleted unit under the name newName, which is the
// unit name without its project for a stack.
func (u deletedUnit) restoreAs(t *testing.T, s GateStore, newName string) error {
	t.Helper()

	e := gateEntry(u.fence.Namespace, "unit:restore")

	var err error

	if u.tc.tool == "pulumi" {
		_, err = s.RestoreStack(t.Context(), stack.ID(u.id), stack.Name(newName), u.fence, e)
	} else {
		_, err = s.RestoreWorkspace(t.Context(), workspace.ID(u.id), workspace.Name(newName), u.fence, e)
	}

	if err != nil {
		return fmt.Errorf("restore unit: %w", err)
	}

	return nil
}

// bareName returns the name of a unit without its project.
func bareName(unit string) string {
	_, bare, found := strings.Cut(unit, "/")
	if !found {
		return unit
	}

	return bare
}

// testGateRestoreNewName checks that a restore under a new name keeps the
// configuration of the unit: the rows key on the stable ID, which the restore
// keeps. A restore onto the name of another deleted unit gets none of the
// configuration of that unit.
func testGateRestoreNewName(t *testing.T, s GateStore, tc gateUnitCase) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	other := tc.unit + "-b"

	for _, unit := range []string{tc.unit, other} {
		if err = d.CreateUnit(t.Context(), "acme", tc.tool, unit, ""); err != nil {
			t.Fatalf("CreateUnit(%s) = %v", unit, err)
		}
	}

	en := gateEntry("acme", "namespace:manage-gates")
	set := gate.PathSet{Scope: gate.NamespaceScope("acme"), ID: "network", Version: 0, Globs: []string{"net/**"}}

	if _, err = s.PathSets().Put(t.Context(), set, 0, fence, en); err != nil {
		t.Fatalf("Put(path set) = %v", err)
	}

	oldScope := unitScopeOf(t, tc.tool, tc.unit)
	otherScope := unitScopeOf(t, tc.tool, other)
	putGateAt(t, s, oldScope, fence)

	limit := gate.AccessLimit{
		Scope: otherScope, ID: "no-agents", Version: 0, Policy: `forbid (principal, action, resource);`,
	}
	if _, err = s.AccessLimits().Put(t.Context(), limit, 0, fence, en); err != nil {
		t.Fatalf("Put(access limit of the other unit) = %v", err)
	}

	deleted := deletedUnit{tc: tc, fence: fence, id: softDeleteUnitID(t, s, tc, fence)}
	softDeleteUnitID(t, s, gateUnitCase{name: "", tool: tc.tool, unit: other}, fence)

	if err = deleted.restoreAs(t, s, bareName(other)); err != nil {
		t.Fatalf("restore onto the name of another deleted unit = %v", err)
	}

	if n := gateCount(t, s, otherScope); n != 1 {
		t.Fatalf("gates of the restored unit = %d, want 1", n)
	}

	limits, err := s.AccessLimits().List(t.Context(), otherScope)
	if err != nil || len(limits) != 0 {
		t.Fatalf("access limits of the restored unit = %d, %v, want none of the other deleted unit", len(limits), err)
	}

	if n := gateCount(t, s, oldScope); n != 0 {
		t.Fatalf("gates of the old name = %d, want 0", n)
	}

	// The gate still reads the path set: the reference stays with the row.
	ref := gate.ObjectRef{Scope: gate.NamespaceScope("acme"), ID: "network", Version: 1}

	err = s.PathSets().Delete(t.Context(), ref, fence, en)
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Delete(path set read by the restored unit's gate) = %v, want gate.ErrInvalid", err)
	}
}

// testGateRestoreAfterReuse checks that a restore under a new name, while a
// live unit holds the old name, gives each unit its own configuration.
func testGateRestoreAfterReuse(t *testing.T, s GateStore, tc gateUnitCase) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	if err = d.CreateUnit(t.Context(), "acme", tc.tool, tc.unit, ""); err != nil {
		t.Fatalf("CreateUnit() = %v", err)
	}

	en := gateEntry("acme", "namespace:manage-gates")
	set := gate.PathSet{Scope: gate.NamespaceScope("acme"), ID: "network", Version: 0, Globs: []string{"net/**"}}

	if _, err = s.PathSets().Put(t.Context(), set, 0, fence, en); err != nil {
		t.Fatalf("Put(path set) = %v", err)
	}

	unit := unitScopeOf(t, tc.tool, tc.unit)
	putGateAt(t, s, unit, fence)

	deleted := deletedUnit{tc: tc, fence: fence, id: softDeleteUnitID(t, s, tc, fence)}

	if err = d.CreateUnit(t.Context(), "acme", tc.tool, tc.unit, ""); err != nil {
		t.Fatalf("CreateUnit(again) = %v", err)
	}

	limit := gate.AccessLimit{Scope: unit, ID: "no-agents", Version: 0, Policy: `forbid (principal, action, resource);`}
	if _, err = s.AccessLimits().Put(t.Context(), limit, 0, fence, en); err != nil {
		t.Fatalf("Put(access limit) = %v", err)
	}

	fresh := tc.unit + "-new"

	if err = deleted.restoreAs(t, s, bareName(fresh)); err != nil {
		t.Fatalf("restore under a new name while a live unit holds the old name = %v", err)
	}

	freshScope := unitScopeOf(t, tc.tool, fresh)

	tests := []struct {
		name   string
		scope  gate.Scope
		gates  int
		limits int
	}{
		{"the live unit", unit, 0, 1},
		{"the restored unit", freshScope, 1, 0},
	}

	for _, tt := range tests {
		if n := gateCount(t, s, tt.scope); n != tt.gates {
			t.Fatalf("gates of %s = %d, want %d", tt.name, n, tt.gates)
		}

		limits, lerr := s.AccessLimits().List(t.Context(), tt.scope)
		if lerr != nil || len(limits) != tt.limits {
			t.Fatalf("access limits of %s = %d, %v, want %d", tt.name, len(limits), lerr, tt.limits)
		}
	}
}

// unitScopeOf returns the gate scope of the unit of the tool.
func unitScopeOf(t *testing.T, tool, unit string) gate.Scope {
	t.Helper()

	scope, err := gate.UnitScope("acme", tool, unit)
	if err != nil {
		t.Fatalf("UnitScope(%s) = %v", unit, err)
	}

	return scope
}

// testGatePurgeChangeCap checks that a purge of a unit with the most gates that
// a scope holds passes when the entry already has a change: the changes of the
// entry stop at the cap and end with the count of the objects left out.
func testGatePurgeChangeCap(t *testing.T, s GateStore) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	tc := gateUnitCase{name: "", tool: "opentofu", unit: "net"}
	if err = d.CreateUnit(t.Context(), "acme", tc.tool, tc.unit, ""); err != nil {
		t.Fatalf("CreateUnit() = %v", err)
	}

	scope, err := gate.UnitScope("acme", tc.tool, tc.unit)
	if err != nil {
		t.Fatalf("UnitScope() = %v", err)
	}

	for i := range gate.MaxGatesPerScope {
		g := gate.Gate{
			Scope: scope, ID: gate.ID(fmt.Sprintf("g%03d", i)), Version: 0, DecisionPoint: gate.PointChangePlanned,
			Condition: "", Unless: "", Outcome: gate.OutcomeDeny, Message: "no", Mode: gate.ModeEvaluate,
			OnError: gate.OnErrorFail, Final: false,
			CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
			State:       "", Reads: nil,
		}

		e := gateEntry("acme", "namespace:manage-gates")
		if _, err = s.Gates().Put(t.Context(), g, 0, fence, e); err != nil {
			t.Fatalf("Put(gate %d) = %v", i, err)
		}
	}

	id := softDeleteUnitID(t, s, tc, fence)

	purge := gateEntry("acme", "unit:purge")
	purge.Changes = []audit.Change{{Key: "reason", Old: nil, New: "cleanup", Layer: "", TakesEffect: ""}}

	if err = s.PurgeWorkspace(t.Context(), workspace.ID(id), fence, time.Time{}, purge); err != nil {
		t.Fatalf("PurgeWorkspace() = %v", err)
	}

	listing, err := s.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("List(audit) = %v", err)
	}

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Action != "unit:purge" {
			continue
		}

		if len(e.Changes) != audit.MaxChanges {
			t.Fatalf("changes of the purge entry = %d, want %d", len(e.Changes), audit.MaxChanges)
		}

		if last := e.Changes[len(e.Changes)-1].Key; last != "removed:more" {
			t.Fatalf("last change of the purge entry = %q, want removed:more", last)
		}

		return
	}

	t.Fatal("no audit entry with the action unit:purge")
}

// testGatePurgeAfterReuse checks that the purge of a deleted unit removes its
// own configuration only: a new unit with the same name starts with none of
// it, and keeps its own configuration through the purge.
func testGatePurgeAfterReuse(t *testing.T, s GateStore, tc gateUnitCase) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	if err = d.CreateUnit(t.Context(), "acme", tc.tool, tc.unit, ""); err != nil {
		t.Fatalf("CreateUnit() = %v", err)
	}

	unit, err := gate.UnitScope("acme", tc.tool, tc.unit)
	if err != nil {
		t.Fatalf("UnitScope() = %v", err)
	}

	en := gateEntry("acme", "namespace:manage-gates")
	set := gate.PathSet{Scope: gate.NamespaceScope("acme"), ID: "network", Version: 0, Globs: []string{"net/**"}}

	if _, err = s.PathSets().Put(t.Context(), set, 0, fence, en); err != nil {
		t.Fatalf("Put(path set) = %v", err)
	}

	putGateAt(t, s, unit, fence)

	purge := softDeleteUnit(t, s, tc, fence)

	// A team creates the unit again under the freed name. It starts clean,
	// and gets an access limit of its own.
	if err = d.CreateUnit(t.Context(), "acme", tc.tool, tc.unit, ""); err != nil {
		t.Fatalf("CreateUnit(again) = %v", err)
	}

	if n := gateCount(t, s, unit); n != 0 {
		t.Fatalf("gates of the new unit = %d, want 0: a new unit with a used name starts clean", n)
	}

	limit := gate.AccessLimit{Scope: unit, ID: "no-agents", Version: 0, Policy: `forbid (principal, action, resource);`}
	if _, err = s.AccessLimits().Put(t.Context(), limit, 0, fence, en); err != nil {
		t.Fatalf("Put(access limit) = %v", err)
	}

	if err = purge(); err != nil {
		t.Fatalf("purge of the old unit = %v", err)
	}

	limits, err := s.AccessLimits().List(t.Context(), unit)
	if err != nil {
		t.Fatalf("List(access limits) = %v", err)
	}

	if len(limits) != 1 {
		t.Fatalf("access limits of the new unit = %d, want 1: the purge of the old unit keeps them", len(limits))
	}

	wantRemoved(t, s, "unit:purge", []string{"removed:gate:cost-cap"})
}

func testGateRenameStack(t *testing.T, s GateStore) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	if err = d.CreateUnit(t.Context(), "acme", "pulumi", "web/prod", ""); err != nil {
		t.Fatalf("CreateUnit() = %v", err)
	}

	oldScope, err := gate.UnitScope("acme", "pulumi", "web/prod")
	if err != nil {
		t.Fatalf("UnitScope() = %v", err)
	}

	newScope, err := gate.UnitScope("acme", "pulumi", "web/live")
	if err != nil {
		t.Fatalf("UnitScope(new) = %v", err)
	}

	ns := gate.NamespaceScope("acme")
	en := gateEntry("acme", "namespace:manage-gates")
	set := gate.PathSet{Scope: ns, ID: "network", Version: 0, Globs: []string{"net/**"}}

	if _, err = s.PathSets().Put(t.Context(), set, 0, fence, en); err != nil {
		t.Fatalf("Put(path set) = %v", err)
	}

	putGateAt(t, s, oldScope, fence)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("ParseRef() = %v", err)
	}

	live, err := stack.ParseName("live")
	if err != nil {
		t.Fatalf("ParseName() = %v", err)
	}

	if err = s.RenameStack(t.Context(), ref, live, fence, renameEntry(ref.Namespace, ref)); err != nil {
		t.Fatalf("RenameStack() = %v", err)
	}

	if n := gateCount(t, s, oldScope); n != 0 {
		t.Fatalf("gates of the old name = %d, want 0", n)
	}

	if n := gateCount(t, s, newScope); n != 1 {
		t.Fatalf("gates of the new name = %d, want 1", n)
	}

	// The gate still reads the path set: the reference stays with the row.
	err = s.PathSets().Delete(t.Context(), gate.ObjectRef{Scope: ns, ID: "network", Version: 1}, fence, en)
	if !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Delete(path set read by the renamed unit's gate) = %v, want gate.ErrInvalid", err)
	}
}

// testGateRenameToUsedName checks that a rename to the name of a deleted unit
// keeps the configuration of the renamed unit, and gives it none of the
// configuration of the deleted unit.
func testGateRenameToUsedName(t *testing.T, s GateStore) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	for _, unit := range []string{"web/other", "web/third"} {
		if err = d.CreateUnit(t.Context(), "acme", "pulumi", unit, ""); err != nil {
			t.Fatalf("CreateUnit(%s) = %v", unit, err)
		}
	}

	en := gateEntry("acme", "namespace:manage-gates")
	set := gate.PathSet{Scope: gate.NamespaceScope("acme"), ID: "network", Version: 0, Globs: []string{"net/**"}}

	if _, err = s.PathSets().Put(t.Context(), set, 0, fence, en); err != nil {
		t.Fatalf("Put(path set) = %v", err)
	}

	otherScope := unitScopeOf(t, "pulumi", "web/other")
	thirdScope := unitScopeOf(t, "pulumi", "web/third")
	putGateAt(t, s, otherScope, fence)

	limit := gate.AccessLimit{
		Scope: thirdScope, ID: "no-agents", Version: 0, Policy: `forbid (principal, action, resource);`,
	}
	if _, err = s.AccessLimits().Put(t.Context(), limit, 0, fence, en); err != nil {
		t.Fatalf("Put(access limit) = %v", err)
	}

	softDeleteUnit(t, s, gateUnitCase{name: "", tool: "pulumi", unit: "web/other"}, fence)

	third, err := stack.ParseRef("acme", "web", "third")
	if err != nil {
		t.Fatalf("ParseRef() = %v", err)
	}

	if err = s.RenameStack(t.Context(), third, "other", fence, renameEntry(third.Namespace, third)); err != nil {
		t.Fatalf("RenameStack(to the name of a deleted unit) = %v", err)
	}

	if n := gateCount(t, s, otherScope); n != 0 {
		t.Fatalf("gates of the renamed unit = %d, want 0: the gate of the deleted unit stays with it", n)
	}

	limits, err := s.AccessLimits().List(t.Context(), otherScope)
	if err != nil || len(limits) != 1 {
		t.Fatalf("access limits of the renamed unit = %d, %v, want 1", len(limits), err)
	}
}

func testGateJoinFinal(t *testing.T, s GateStore, tc gateUnitCase) {
	t.Helper()

	d := gateDomain{s: s}

	fence, err := d.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace() = %v", err)
	}

	if err = d.CreateCollection(t.Context(), "acme", "prod"); err != nil {
		t.Fatalf("CreateCollection() = %v", err)
	}

	for _, unit := range []string{tc.unit, tc.unit + "-b"} {
		if err = d.CreateUnit(t.Context(), "acme", tc.tool, unit, ""); err != nil {
			t.Fatalf("CreateUnit(%s) = %v", unit, err)
		}
	}

	en := gateEntry("acme", "namespace:manage-gates")
	set := gate.PathSet{Scope: gate.NamespaceScope("acme"), ID: "network", Version: 0, Globs: []string{"net/**"}}

	if _, err = s.PathSets().Put(t.Context(), set, 0, fence, en); err != nil {
		t.Fatalf("Put(path set) = %v", err)
	}

	holder, err := gate.UnitScope("acme", tc.tool, tc.unit)
	if err != nil {
		t.Fatalf("UnitScope() = %v", err)
	}

	putGateAt(t, s, holder, fence)

	prod := gate.CollectionScope("acme", "prod")
	final := gate.Gate{
		Scope: prod, ID: "cost-cap", Version: 0, DecisionPoint: gate.PointChangePlanned, Condition: "", Unless: "",
		Outcome: gate.OutcomeDeny, Message: "no", Mode: gate.ModeEnforce, OnError: gate.OnErrorFail, Final: true,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:       "", Reads: nil,
	}

	if _, err = s.Gates().Put(t.Context(), final, 0, fence, en); err != nil {
		t.Fatalf("Put(final gate) = %v", err)
	}

	target, err := s.GetCollection(t.Context(), collection.Ref{Namespace: "acme", Name: "prod"})
	if err != nil {
		t.Fatalf("GetCollection() = %v", err)
	}

	tests := []struct {
		name string
		unit string
		want error
	}{
		{"a unit with a gate of the same ID", tc.unit, gate.ErrFinal},
		{"a unit with no gate", tc.unit + "-b", nil},
	}

	for _, tt := range tests {
		move := collection.Move{
			Unit: collection.UnitRef{Namespace: "acme", Tool: tc.tool, Name: tt.unit}, From: "", To: target.Ref,
			ToID: target.ID, EndsLock: false,
		}

		err = s.SetUnitCollection(t.Context(), move, fence, gateEntry("acme", "unit:set-collection"))
		if !errors.Is(err, tt.want) {
			t.Fatalf("SetUnitCollection(%s) = %v, want %v", tt.name, err, tt.want)
		}
	}
}
