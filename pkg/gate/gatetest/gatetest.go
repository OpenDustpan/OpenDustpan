// Package gatetest holds the conformance cases of the ports of package
// gate: the store ports (gate.Objects, gate.Settings, gate.Store and
// gate.Records) and the policy engine port (gate.Engine).
//
// RunObjects executes the rules of gate.Objects for one scope against a
// store adapter. The other cases are Go values: the port method that each
// one calls, the kinds, scopes and policy IDs that it uses, the error that
// it wants, and the behavior that it must check. A rename of a port method
// or of a fixture breaks the build of this package. The first store adapter
// and the first engine adapter add the Run functions that execute each
// named case, and the store in memory in the tests of this package runs
// them first.
//
// In the cases, the unit web/prod of the tool pulumi is in the collection
// prod of acme, and the unit net of the tool opentofu is in no collection.
// The configuration of a unit keys on its stable ID, not on its name: it
// stays with the unit through a rename and a move between collections, and a
// new unit with the name of a deleted unit starts with none.
package gatetest

import (
	"slices"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Port names a port of package gate.
type Port string

// The ports that the cases check.
const (
	PortObjects  Port = "Objects"
	PortSettings Port = "Settings"
	PortStore    Port = "Store"
	PortRecords  Port = "Records"
)

// Kind names one kind of gate.Objects. A case of PortObjects with no kind
// runs for each kind.
type Kind string

// The kinds of gate.Objects.
const (
	KindGate          Kind = "gate"
	KindAccessLimit   Kind = "access-limit"
	KindApprovalRule  Kind = "approval-rule"
	KindOwnershipRule Kind = "ownership-rule"
	KindPathSet       Kind = "path-set"
)

// StoreCase is one conformance case of a store port.
type StoreCase struct {
	// Name is the subtest name. It is unique in its list.
	Name string
	// Port is the port of Method.
	Port Port
	// Method is the method expression of the port method that the case
	// calls first, for example gate.Settings.PutCarryOver. A case of
	// PortObjects with no Kinds names the method of gate.Objects[gate.Gate]
	// for each kind.
	Method any
	// Kinds limits a case of PortObjects to these kinds. Empty means each
	// kind.
	Kinds []Kind
	// Scopes holds the scopes that the case writes or reads.
	Scopes []gate.Scope
	// Policies holds the policy IDs that the case uses.
	Policies []gate.PolicyID
	// Want is the sentinel that the case checks with errors.Is, or nil for
	// a case that commits.
	Want error
	// Checks is the behavior that the case must check.
	Checks string
}

// EngineCase is one conformance case of gate.Engine. Each adapter runs each
// case. The cases whose name starts with "external-" run for the adapter of
// an external service only.
type EngineCase struct {
	Name     string
	Method   any
	Scopes   []gate.Scope
	Policies []gate.PolicyID
	Want     error
	Checks   string
}

// fixtures holds the scopes of the cases.
type fixtures struct {
	installation, acme, globex, prod, staging              gate.Scope
	webProd, acmeNet, globexNet, blueprintWeb, profileBase gate.Scope
	// webLive is the scope of web/prod after a rename to web/live, and
	// ghost is the scope of a unit that no namespace holds.
	webLive, ghost gate.Scope
}

// newFixtures returns the scopes of the cases.
func newFixtures() fixtures {
	return fixtures{
		installation: gate.InstallationScope(),
		acme:         gate.NamespaceScope("acme"),
		globex:       gate.NamespaceScope("globex"),
		prod:         gate.CollectionScope("acme", "prod"),
		staging:      gate.CollectionScope("acme", "staging"),
		webProd:      must(gate.UnitScope("acme", "pulumi", "web/prod")),
		acmeNet:      must(gate.UnitScope("acme", "opentofu", "net")),
		globexNet:    must(gate.UnitScope("globex", "opentofu", "net")),
		blueprintWeb: must(gate.BlueprintScope("web", 1)),
		profileBase:  must(gate.ProfileScope("acme", "base")),
		webLive:      must(gate.UnitScope("acme", "pulumi", "web/live")),
		ghost:        must(gate.UnitScope("acme", "opentofu", "ghost")),
	}
}

// must returns s, and panics on err. The scopes of the cases are fixed, so
// a panic is a fault of this package that its tests find.
func must(s gate.Scope, err error) gate.Scope {
	if err != nil {
		panic(err)
	}

	return s
}

// pid returns the policy ID of id at scope.
func pid(scope gate.Scope, id gate.ID) gate.PolicyID {
	return gate.PolicyID{Scope: scope, ID: id}
}

// StoreCases returns the conformance cases of the store ports.
func StoreCases() []StoreCase {
	fx := newFixtures()

	return slices.Concat(objectCases(&fx), objectRuleCases(&fx), unitCases(&fx), settingsCases(&fx), recordCases(&fx))
}

// unitCases returns the cases of the configuration of a unit: it keys on the
// stable ID of the unit, so it follows the unit and never its name.
func unitCases(fx *fixtures) []StoreCase {
	return []StoreCase{
		{
			Name: "unit-reused-name-starts-clean", Port: PortObjects, Method: gate.Objects[gate.Gate].List, Kinds: nil,
			Scopes: []gate.Scope{fx.webProd, fx.acmeNet}, Policies: nil, Want: nil,
			Checks: "After a delete of a unit, a new unit with the same tool and name has no objects: List of its " +
				"scope returns none, Get returns ErrNotFound, and a Put with prev 0 of the old ID returns version 1.",
		},
		{
			Name: "unit-deleted-scope-not-found", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.acmeNet, fx.ghost}, Policies: nil, Want: gate.ErrNotFound,
			Checks: "Put at the scope of a deleted unit, and at the scope of a unit that the namespace never held, " +
				"returns ErrNotFound and changes nothing. List of each scope returns none.",
		},
		{
			Name: "unit-rename-keeps-objects", Port: PortObjects, Method: gate.Objects[gate.Gate].Get, Kinds: nil,
			Scopes: []gate.Scope{fx.webProd, fx.webLive}, Policies: nil, Want: nil,
			Checks: "After a rename of web/prod to web/live, the scope of web/live holds the objects of the unit " +
				"with their versions, and the scope of web/prod holds none.",
		},
		{
			Name: "unit-move-keeps-objects", Port: PortObjects, Method: gate.Objects[gate.Gate].Get, Kinds: nil,
			Scopes: []gate.Scope{fx.webProd, fx.prod, fx.staging}, Policies: nil, Want: nil,
			Checks: "After a move of web/prod from prod to staging, and then back to prod, the scope of the " +
				"unit holds its objects with their versions, and no collection scope holds them.",
		},
		{
			Name: "unit-settings-reused-name-start-clean", Port: PortSettings, Method: gate.Settings.ApprovalDefault,
			Kinds: nil, Scopes: []gate.Scope{fx.webProd}, Policies: nil, Want: gate.ErrNotFound,
			Checks: "After a delete of a unit, a new unit with the same name has no approval default " +
				"(ErrNotFound), and its carry-over setting is off with version 0.",
		},
		{
			Name: "unit-id-scope-reads-the-deleted-unit", Port: PortObjects, Method: gate.Objects[gate.Gate].List,
			Kinds: nil, Scopes: []gate.Scope{fx.webProd}, Policies: nil, Want: nil,
			Checks: "A unit scope with the stable ID of a deleted unit reads the objects of that unit, with no live " +
				"unit of the name and with a new live unit of the name. The scope by name reads the new unit only.",
		},
		{
			Name: "unit-id-scope-refused", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.webProd, fx.acmeNet}, Policies: nil, Want: gate.ErrInvalid,
			Checks: "Put at a unit scope with a stable ID returns ErrInvalid: a write names a unit by its name. A " +
				"read with an ID that names no unit, or a unit of another tool, returns ErrInvalid.",
		},
		{
			Name: "deleted-unit-reader-label", Port: PortObjects, Method: gate.Objects[gate.ApprovalRule].Delete,
			Kinds: []Kind{KindApprovalRule}, Scopes: []gate.Scope{fx.acme, fx.webProd}, Policies: nil,
			Want: gate.ErrInvalid,
			Checks: "Delete of an approval rule that a gate of a deleted unit names is refused, and the refusal " +
				"names the deleted unit as deleted-unit/<tool>/<unit ID>, not by the name that a new unit holds.",
		},
		{
			Name: "snapshot-deleted-unit-by-id", Port: PortStore, Method: gate.Store.Snapshot, Kinds: nil,
			Scopes: []gate.Scope{fx.installation, fx.acme, fx.webProd}, Policies: nil, Want: nil,
			Checks: "Snapshot of a chain whose unit scope carries the stable ID of a deleted unit holds the objects " +
				"and the settings of that unit, and none of a new unit with its name.",
		},
		{
			Name: "snapshot-reused-name-starts-clean", Port: PortStore, Method: gate.Store.Snapshot, Kinds: nil,
			Scopes: []gate.Scope{fx.installation, fx.acme, fx.prod, fx.webProd}, Policies: nil, Want: nil,
			Checks: "Snapshot of the chain of a new unit with the name of a deleted unit holds no object and no " +
				"setting of the deleted unit, and holds the objects of the wider scopes.",
		},
	}
}

// objectCases returns the cases of gate.Objects that hold for each kind.
func objectCases(fx *fixtures) []StoreCase {
	return []StoreCase{
		{
			Name: "put-new-has-version-1", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: nil,
			Checks: "Put with prev 0 of a new ID returns version 1, and Get returns the object with version 1.",
		},
		{
			Name:     "put-new-of-existing-id-conflicts",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Put,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrConflict,
			Checks:   "Put with prev 0 of an ID that the scope holds returns ErrConflict and changes nothing.",
		},
		{
			Name: "put-replace-increments-version", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: nil,
			Checks: "Put with prev equal to the stored version replaces the object and returns that version plus 1.",
		},
		{
			Name: "put-stale-version-conflicts", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: gate.ErrConflict,
			Checks: "Put with prev that is not the stored version returns ErrConflict and changes nothing.",
		},
		{
			Name: "put-replace-unknown-not-found", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: gate.ErrNotFound,
			Checks: "Put with prev above 0 of an ID that the scope does not hold returns ErrNotFound.",
		},
		{
			Name: "same-id-in-two-scopes", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.acmeNet, fx.globexNet}, Policies: nil, Want: nil,
			Checks: "Two scopes each hold an object with one ID, and Get of each scope returns its own object.",
		},
		{
			Name: "unit-name-with-slash", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.webProd}, Policies: nil, Want: nil,
			Checks: "An object of the unit web/prod is stored and read back with the same scope.",
		},
		{
			Name: "get-other-scope-not-found", Port: PortObjects, Method: gate.Objects[gate.Gate].Get, Kinds: nil,
			Scopes: []gate.Scope{fx.acme, fx.globex}, Policies: nil, Want: gate.ErrNotFound,
			Checks: "Get of an ID that only another scope holds returns ErrNotFound.",
		},
		{
			Name: "list-own-scope-by-id", Port: PortObjects, Method: gate.Objects[gate.Gate].List, Kinds: nil,
			Scopes: []gate.Scope{fx.installation, fx.acme, fx.prod}, Policies: nil, Want: nil,
			Checks: "List returns the objects of its scope only, ordered by ID, and none of a wider or narrower scope.",
		},
		{
			Name:     "list-every-scope-by-id",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].ListEveryScope,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.installation, fx.acme, fx.globex},
			Policies: nil,
			Want:     nil,
			Checks: "ListEveryScope returns the objects of its kind of every scope, with the objects of one scope " +
				"ordered by ID, and none of another kind.",
		},
		{
			Name: "delete-removes", Port: PortObjects, Method: gate.Objects[gate.Gate].Delete, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: nil,
			Checks: "Delete with the stored version removes the object, and Get then returns ErrNotFound.",
		},
		{
			Name:     "delete-stale-version-conflicts",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Delete,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrConflict,
			Checks:   "Delete with another version returns ErrConflict and keeps the object.",
		},
		{
			Name: "delete-unknown-not-found", Port: PortObjects, Method: gate.Objects[gate.Gate].Delete, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: gate.ErrNotFound,
			Checks: "Delete of an ID that the scope does not hold returns ErrNotFound.",
		},
		{
			Name: "delete-other-scope-not-found", Port: PortObjects, Method: gate.Objects[gate.Gate].Delete, Kinds: nil,
			Scopes: []gate.Scope{fx.acme, fx.globex}, Policies: nil, Want: gate.ErrNotFound,
			Checks: "Delete of an ID that only another scope holds returns ErrNotFound and keeps the object.",
		},
		{
			Name:     "installation-takes-zero-fence",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Put,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.installation},
			Policies: nil,
			Want:     nil,
			Checks:   "A write at the installation with the zero fence and an entry in audit.ServerScope commits and appends the entry.",
		},
		{
			Name:     "stale-fence-changes-nothing",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Put,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.prod},
			Policies: nil,
			Want:     namespace.ErrFenced,
			Checks:   "After a bump of the failover version, Put and Delete with the old fence return namespace.ErrFenced, change nothing and append no entry.",
		},
		{
			Name:     "fence-of-other-namespace-refused",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Put,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme, fx.globex},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "A write at a scope of acme with the fence of globex, or with the zero fence, returns ErrInvalid and changes nothing.",
		},
		{
			Name:     "entry-of-other-scope-refused",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Put,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     audit.ErrInvalidEntry,
			Checks:   "A write with an audit entry of another scope, or an entry that fails validation, returns audit.ErrInvalidEntry, changes nothing and appends nothing.",
		},
		{
			Name: "entry-in-same-transaction", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: nil,
			Checks: "Each committed write appends exactly its entry, and each refused write appends none.",
		},
		{
			Name: "scope-without-routes-refused", Port: PortObjects, Method: gate.Objects[gate.Gate].Put, Kinds: nil,
			Scopes: []gate.Scope{fx.blueprintWeb, fx.profileBase}, Policies: nil, Want: gate.ErrInvalid,
			Checks: "A write at a blueprint version or a profile scope returns ErrInvalid.",
		},
		{
			Name:     "scope-limit",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Put,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrLimit,
			Checks:   "A new object above the limit of its kind for one scope returns ErrLimit, and a replace at the limit commits.",
		},
	}
}

// objectRuleCases returns the cases of gate.Objects that hold for one kind.
func objectRuleCases(fx *fixtures) []StoreCase {
	return []StoreCase{
		{
			Name:   "final-gate-of-wider-scope-shadow",
			Port:   PortObjects,
			Method: gate.Objects[gate.Gate].Put,
			Kinds:  []Kind{KindGate},
			Scopes: []gate.Scope{
				fx.installation,
				fx.acme,
				fx.prod,
				fx.webProd,
			},
			Policies: []gate.PolicyID{pid(fx.installation, "no-destroy"), pid(fx.prod, "no-destroy")},
			Want:     gate.ErrFinal,
			Checks:   "A gate with the ID of a final gate of a wider scope returns ErrFinal at each narrower scope, also at a unit of the collection of the final gate.",
		},
		{
			Name:   "final-gate-of-unrelated-collection",
			Port:   PortObjects,
			Method: gate.Objects[gate.Gate].Put,
			Kinds:  []Kind{KindGate},
			Scopes: []gate.Scope{
				fx.staging,
				fx.webProd,
			},
			Policies: []gate.PolicyID{pid(fx.staging, "no-destroy"), pid(fx.webProd, "no-destroy")},
			Want:     nil,
			Checks:   "A final gate of a collection that is not the collection of the unit does not stop a gate of the unit with its ID.",
		},
		{
			Name:   "non-final-gate-of-wider-scope",
			Port:   PortObjects,
			Method: gate.Objects[gate.Gate].Put,
			Kinds:  []Kind{KindGate},
			Scopes: []gate.Scope{
				fx.acme,
				fx.webProd,
			},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap"), pid(fx.webProd, "cost-cap")},
			Want:     nil,
			Checks:   "A gate with the ID of a gate of a wider scope that is not final commits, at either scope first.",
		},
		{
			Name:   "final-gate-over-narrower-gate",
			Port:   PortObjects,
			Method: gate.Objects[gate.Gate].Put,
			Kinds:  []Kind{KindGate},
			Scopes: []gate.Scope{
				fx.acme,
				fx.prod,
				fx.webProd,
			},
			Policies: []gate.PolicyID{pid(fx.acme, "no-destroy"), pid(fx.webProd, "no-destroy")},
			Want:     gate.ErrFinal,
			Checks:   "A save at acme of a final gate, or a change that sets Final, returns ErrFinal when a narrower scope holds a gate with its ID, and the error names that scope.",
		},
		{
			Name:   "gate-rule-must-resolve",
			Port:   PortObjects,
			Method: gate.Objects[gate.Gate].Put,
			Kinds:  []Kind{KindGate},
			Scopes: []gate.Scope{
				fx.acme,
				fx.prod,
			},
			Policies: []gate.PolicyID{pid(fx.acme, "dba-review")},
			Want:     gate.ErrInvalid,
			Checks:   "A gate whose outcome names a rule that its scope and each wider scope do not hold returns ErrInvalid. A rule of a narrower scope does not resolve.",
		},
		{
			Name:   "gate-path-sets-must-resolve",
			Port:   PortObjects,
			Method: gate.Objects[gate.Gate].Put,
			Kinds:  []Kind{KindGate},
			Scopes: []gate.Scope{
				fx.acme,
			},
			Policies: []gate.PolicyID{pid(fx.acme, "network-only")},
			Want:     gate.ErrInvalid,
			Checks:   "A gate whose Reads names a path set that does not resolve returns ErrInvalid.",
		},
		{
			Name:     "final-gate-weaker-needs-reason",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Put,
			Kinds:    []Kind{KindGate},
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "no-destroy")},
			Want:     gate.ErrFinal,
			Checks:   "A change of a final gate that is weaker (a switch to evaluate, a clear of Final, or a change of the decision point, the condition, Unless, the outcome or OnError), or its Delete, with no reason in the audit entry returns ErrFinal. With a reason it commits, and the entry holds the reason.",
		},
		{
			Name:     "gate-state-not-stored",
			Port:     PortObjects,
			Method:   gate.Objects[gate.Gate].Put,
			Kinds:    []Kind{KindGate},
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap")},
			Want:     nil,
			Checks:   "Put keeps CheckedWith and ignores State, and Get returns the empty State.",
		},
		{
			Name:     "rule-of-wider-scope-shadow",
			Port:     PortObjects,
			Method:   gate.Objects[gate.ApprovalRule].Put,
			Kinds:    []Kind{KindApprovalRule},
			Scopes:   []gate.Scope{fx.installation, fx.acme},
			Policies: nil,
			Want:     gate.ErrFinal,
			Checks:   "An approval rule with the ID of a rule of a wider scope returns ErrFinal.",
		},
		{
			Name:     "rule-fields-must-be-valid",
			Port:     PortObjects,
			Method:   gate.Objects[gate.ApprovalRule].Put,
			Kinds:    []Kind{KindApprovalRule},
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "Put of an approval rule with a count outside 1 to 10, no approver, an empty approver name, a window outside 0 and 60 to 900 seconds, or a timeout outside 1 hour to 30 days returns ErrInvalid, stores nothing and appends no entry.",
		},
		{
			Name:     "rule-over-narrower-rule",
			Port:     PortObjects,
			Method:   gate.Objects[gate.ApprovalRule].Put,
			Kinds:    []Kind{KindApprovalRule},
			Scopes:   []gate.Scope{fx.acme, fx.webProd},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "An approval rule at acme with the ID of a rule of a narrower scope returns ErrInvalid, and the error names that scope.",
		},
		{
			Name:   "rule-named-by-gate-delete",
			Port:   PortObjects,
			Method: gate.Objects[gate.ApprovalRule].Delete,
			Kinds:  []Kind{KindApprovalRule},
			Scopes: []gate.Scope{
				fx.acme,
				fx.webProd,
			},
			Policies: []gate.PolicyID{pid(fx.webProd, "dba-review")},
			Want:     gate.ErrInvalid,
			Checks:   "Delete of a rule that a stored gate names returns ErrInvalid, the error names the policy ID of each gate, and the rule stays.",
		},
		{
			Name:     "rule-of-final-gate-needs-reason",
			Port:     PortObjects,
			Method:   gate.Objects[gate.ApprovalRule].Put,
			Kinds:    []Kind{KindApprovalRule},
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "no-destroy")},
			Want:     gate.ErrFinal,
			Checks:   "Put of an approval rule that a final gate names, with no reason in the audit entry, returns ErrFinal. With a reason it commits.",
		},
		{
			Name:     "path-set-of-wider-scope-shadow",
			Port:     PortObjects,
			Method:   gate.Objects[gate.PathSet].Put,
			Kinds:    []Kind{KindPathSet},
			Scopes:   []gate.Scope{fx.acme, fx.prod},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "A path set with the ID of a path set of a wider scope returns ErrInvalid.",
		},
		{
			Name:     "path-set-over-narrower-path-set",
			Port:     PortObjects,
			Method:   gate.Objects[gate.PathSet].Put,
			Kinds:    []Kind{KindPathSet},
			Scopes:   []gate.Scope{fx.acme, fx.prod},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "A path set at acme with the ID of a path set of a narrower scope returns ErrInvalid, and the error names that scope.",
		},
		{
			Name:   "path-set-read-delete",
			Port:   PortObjects,
			Method: gate.Objects[gate.PathSet].Delete,
			Kinds:  []Kind{KindPathSet},
			Scopes: []gate.Scope{
				fx.acme,
			},
			Policies: []gate.PolicyID{pid(fx.acme, "network-only")},
			Want:     gate.ErrInvalid,
			Checks:   "Delete of a path set that a stored gate, approval default or ownership rule reads returns ErrInvalid, names each reader, and the path set stays.",
		},
		{
			Name:     "path-set-of-final-gate-needs-reason",
			Port:     PortObjects,
			Method:   gate.Objects[gate.PathSet].Put,
			Kinds:    []Kind{KindPathSet},
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "network-only")},
			Want:     gate.ErrFinal,
			Checks:   "Put of a path set that a final gate reads, with no reason in the audit entry, returns ErrFinal. With a reason it commits.",
		},
		{
			Name:     "ownership-path-sets-must-resolve",
			Port:     PortObjects,
			Method:   gate.Objects[gate.OwnershipRule].Put,
			Kinds:    []Kind{KindOwnershipRule},
			Scopes:   []gate.Scope{fx.prod},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "An ownership rule whose Reads names a path set that does not resolve returns ErrInvalid.",
		},
		{
			Name:   "concurrent-shadow",
			Port:   PortObjects,
			Method: gate.Objects[gate.Gate].Put,
			Kinds:  []Kind{KindGate},
			Scopes: []gate.Scope{
				fx.acme,
				fx.webProd,
			},
			Policies: []gate.PolicyID{pid(fx.acme, "no-destroy"), pid(fx.webProd, "no-destroy")},
			Want:     gate.ErrFinal,
			Checks:   "A final gate of acme and a gate of web/prod with the same ID, saved at the same time, never both commit.",
		},
	}
}

// settingsCases returns the cases of gate.Settings and gate.Store.
func settingsCases(fx *fixtures) []StoreCase {
	return []StoreCase{
		{
			Name: "approval-default-none", Port: PortSettings, Method: gate.Settings.ApprovalDefault, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: gate.ErrNotFound,
			Checks: "A scope with no approval default returns ErrNotFound.",
		},
		{
			Name:     "approval-default-round-trip",
			Port:     PortSettings,
			Method:   gate.Settings.PutApprovalDefault,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme, fx.prod, fx.webProd},
			Policies: nil,
			Want:     nil,
			Checks:   "PutApprovalDefault stores each field, follows the version rules of Objects.Put, and appends its entry.",
		},
		{
			Name:     "approval-default-fields-must-be-valid",
			Port:     PortSettings,
			Method:   gate.Settings.PutApprovalDefault,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "PutApprovalDefault with a count outside 1 to 10, a window outside 0 and 60 to 900 seconds, or an empty approver name returns ErrInvalid, stores nothing and appends no entry.",
		},
		{
			Name:     "approval-default-not-at-installation",
			Port:     PortSettings,
			Method:   gate.Settings.PutApprovalDefault,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.installation},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "PutApprovalDefault at the installation returns ErrInvalid.",
		},
		{
			Name:     "approval-default-path-sets-must-resolve",
			Port:     PortSettings,
			Method:   gate.Settings.PutApprovalDefault,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.prod},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "An approval default whose Reads names a path set that does not resolve returns ErrInvalid.",
		},
		{
			Name:     "approval-default-delete",
			Port:     PortSettings,
			Method:   gate.Settings.DeleteApprovalDefault,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     nil,
			Checks:   "DeleteApprovalDefault with the stored version removes the default, appends its entry, and ApprovalDefault then returns ErrNotFound.",
		},
		{
			Name:     "approval-default-delete-none",
			Port:     PortSettings,
			Method:   gate.Settings.DeleteApprovalDefault,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrNotFound,
			Checks:   "DeleteApprovalDefault of a scope with no default returns ErrNotFound.",
		},
		{
			Name:     "approval-default-delete-stale",
			Port:     PortSettings,
			Method:   gate.Settings.DeleteApprovalDefault,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrConflict,
			Checks:   "DeleteApprovalDefault with another version returns ErrConflict and keeps the default.",
		},
		{
			Name:     "approval-default-delete-at-installation",
			Port:     PortSettings,
			Method:   gate.Settings.DeleteApprovalDefault,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.installation},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "DeleteApprovalDefault at the installation returns ErrInvalid.",
		},
		{
			Name: "carry-over-default-off", Port: PortSettings, Method: gate.Settings.CarryOver, Kinds: nil,
			Scopes: []gate.Scope{fx.installation, fx.acme}, Policies: nil, Want: nil,
			Checks: "A scope with no stored setting returns off with version 0.",
		},
		{
			Name: "carry-over-round-trip", Port: PortSettings, Method: gate.Settings.PutCarryOver, Kinds: nil,
			Scopes: []gate.Scope{fx.acme, fx.prod, fx.webProd}, Policies: nil, Want: nil,
			Checks: "PutCarryOver stores on and forbidden with the version rules of Objects.Put.",
		},
		{
			Name:     "carry-over-off-deletes",
			Port:     PortSettings,
			Method:   gate.Settings.PutCarryOver,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     nil,
			Checks:   "PutCarryOver of off deletes the stored setting and returns version 0, CarryOver then returns off with version 0, and the next put takes prev 0.",
		},
		{
			Name: "carry-over-on-at-installation", Port: PortSettings, Method: gate.Settings.PutCarryOver, Kinds: nil,
			Scopes: []gate.Scope{fx.installation}, Policies: nil, Want: gate.ErrInvalid,
			Checks: "PutCarryOver of on at the installation returns ErrInvalid, and forbidden commits.",
		},
		{
			Name: "settings-fence", Port: PortSettings, Method: gate.Settings.PutCarryOver, Kinds: nil,
			Scopes: []gate.Scope{fx.prod}, Policies: nil, Want: namespace.ErrFenced,
			Checks: "A setting write with a stale fence returns namespace.ErrFenced and changes nothing.",
		},
		{
			Name:     "snapshot-chain-in-order",
			Port:     PortStore,
			Method:   gate.Store.Snapshot,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.installation, fx.acme, fx.prod, fx.webProd},
			Policies: nil,
			Want:     nil,
			Checks:   "Snapshot returns each kind and each setting of each named scope, in the order of the scopes and then by ID, and none of a scope that is not named.",
		},
		{
			Name: "snapshot-one-transaction", Port: PortStore, Method: gate.Store.Snapshot, Kinds: nil,
			Scopes: []gate.Scope{fx.acme, fx.webProd}, Policies: nil, Want: nil,
			Checks: "A Snapshot that runs while a write commits sees all of the write or none of it.",
		},
		{
			Name: "snapshot-zero-scope", Port: PortStore, Method: gate.Store.Snapshot, Kinds: nil,
			Scopes: nil, Policies: nil, Want: gate.ErrInvalid,
			Checks: "Snapshot with a zero scope returns ErrInvalid.",
		},
	}
}

// recordCases returns the cases of gate.Records.
func recordCases(fx *fixtures) []StoreCase {
	return []StoreCase{
		{
			Name:   "record-round-trip",
			Port:   PortRecords,
			Method: gate.Records.Put,
			Kinds:  nil,
			Scopes: []gate.Scope{
				fx.acme,
				fx.webProd,
			},
			Policies: []gate.PolicyID{pid(fx.installation, "builtin/plan-readable"), pid(fx.webProd, "cost-cap")},
			Want:     nil,
			Checks:   "Put and Get keep each field: the fact document of each unit, the path set versions, each policy result, the requirements with carried signatures, the fixed controls and the result.",
		},
		{
			Name: "record-other-namespace-not-found", Port: PortRecords, Method: gate.Records.Get, Kinds: nil,
			Scopes: []gate.Scope{fx.acme, fx.globex}, Policies: nil, Want: gate.ErrNotFound,
			Checks: "Get of a record of acme with the namespace globex returns ErrNotFound.",
		},
		{
			Name: "record-used-id", Port: PortRecords, Method: gate.Records.Put, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: gate.ErrConflict,
			Checks: "Put of a used record ID returns ErrConflict and keeps the first record.",
		},
		{
			Name:     "record-fence",
			Port:     PortRecords,
			Method:   gate.Records.Put,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme, fx.globex},
			Policies: nil,
			Want:     namespace.ErrFenced,
			Checks:   "Put with a stale fence returns namespace.ErrFenced, and with the fence of another namespace ErrInvalid.",
		},
		{
			Name:   "record-of-installation-object",
			Port:   PortRecords,
			Method: gate.Records.NoteRefusal,
			Kinds:  nil,
			Scopes: []gate.Scope{
				fx.installation,
			},
			Policies: []gate.PolicyID{pid(fx.installation, "no-agent-tokens")},
			Want:     nil,
			Checks:   "A refusal of an access limit on an installation object is stored with the zero fence and the namespace audit.ServerScope, and Get with audit.ServerScope reads it.",
		},
		{
			Name: "list-by-change-oldest-first", Port: PortRecords, Method: gate.Records.ListByChange, Kinds: nil,
			Scopes: []gate.Scope{fx.acme}, Policies: nil, Want: nil,
			Checks: "ListByChange returns the records of one change, oldest first, and none of another change.",
		},
		{
			Name:     "query-window-and-limit",
			Port:     PortRecords,
			Method:   gate.Records.Query,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "Query returns the records of its point with At in [From, To), newest first, at most Limit, and returns ErrInvalid for a limit of 0 or above 1000.",
		},
		{
			Name:     "note-refusal-coalesces",
			Port:     PortRecords,
			Method:   gate.Records.NoteRefusal,
			Kinds:    nil,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "no-agent-secrets")},
			Want:     nil,
			Checks:   "A second refusal of the same subject, action and limit within AccessRefusalWindow returns the first ID, adds 1 to Count and sets LastAt.",
		},
		{
			Name:   "note-refusal-new-record",
			Port:   PortRecords,
			Method: gate.Records.NoteRefusal,
			Kinds:  nil,
			Scopes: []gate.Scope{
				fx.acme,
			},
			Policies: []gate.PolicyID{pid(fx.acme, "no-agent-secrets"), pid(fx.acme, "no-destroy")},
			Want:     nil,
			Checks:   "A refusal after the window, of another subject, action or limit, stores a new record with Count 1.",
		},
	}
}

// EngineCases returns the conformance cases of the policy engine port.
func EngineCases() []EngineCase {
	fx := newFixtures()

	return append(engineEvaluateCases(&fx), engineCheckCases(&fx)...)
}

// engineEvaluateCases returns the conformance cases of Version and Evaluate.
func engineEvaluateCases(fx *fixtures) []EngineCase {
	return []EngineCase{
		{
			Name: "version-names-engine", Method: gate.Engine.Version, Scopes: nil, Policies: nil, Want: nil,
			Checks: "Version returns a text that is not empty and that is the same for each call.",
		},
		{
			Name: "empty-set-matches-nothing", Method: gate.Engine.Evaluate, Scopes: nil, Policies: nil, Want: nil,
			Checks: "An empty policy set gives one answer for each request, with no match and no diagnostic.",
		},
		{
			Name:     "condition-matches",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap")},
			Want:     nil,
			Checks:   "A gate with context.cost.available && context.cost.deltaMonthly > 10000 matches a document with a delta of 15000 and not one of 5000.",
		},
		{
			Name:     "empty-condition-is-true",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "always")},
			Want:     nil,
			Checks:   "A gate with an empty condition matches each request of its decision point.",
		},
		{
			Name:     "unless-exempts",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "no-destroy")},
			Want:     nil,
			Checks:   "A gate that matches does not match when its unless is true.",
		},
		{
			Name:     "action-is-pinned",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap")},
			Want:     nil,
			Checks:   "A gate of change.planned never matches a request of change.apply or state.write.",
		},
		{
			Name:   "matched-ids-keep-scope",
			Method: gate.Engine.Evaluate,
			Scopes: []gate.Scope{fx.installation, fx.acme, fx.webProd},
			Policies: []gate.PolicyID{
				pid(fx.installation, "cost-cap"),
				pid(fx.acme, "cost-cap"),
				pid(fx.webProd, "cost-cap"),
			},
			Want:   nil,
			Checks: "Three gates with one ID in three scopes that each match are three distinct matched policy IDs, equal to the IDs of the set.",
		},
		{
			Name:     "builtin-id",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.installation},
			Policies: []gate.PolicyID{pid(fx.installation, "builtin/plan-readable")},
			Want:     nil,
			Checks:   "A built-in gate matches with its policy ID installation/builtin/plan-readable.",
		},
		{
			Name:     "principal-groups-are-parents",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "sre-only")},
			Want:     nil,
			Checks:   "A gate with unless { principal in OpenDustpan::Group::\"sre\" } does not match a principal with the group sre.",
		},
		{
			Name:     "path-set-attributes",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.prod},
			Policies: []gate.PolicyID{pid(fx.prod, "network-only")},
			Want:     nil,
			Checks:   "A gate that reads context.paths.available && context.paths.only[\"network\"] matches when Go gave only true for the path set network.",
		},
		{
			Name:     "error-is-diagnostic",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "overflow"), pid(fx.acme, "cost-cap")},
			Want:     nil,
			Checks:   "A gate that gives an evaluation error, for example an overflow, is a diagnostic and not a match, the other gates still match, and the call returns no error.",
		},
		{
			Name:     "limit-error-is-diagnostic",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.prod},
			Policies: []gate.PolicyID{pid(fx.prod, "auth-overflow")},
			Want:     nil,
			Checks:   "An access limit that gives an evaluation error, for example an overflow of context.authAgeSeconds, gives no match and one diagnostic, and the call returns no error.",
		},
		{
			Name:     "duplicate-policy-id-is-invalid",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "always")},
			Want:     nil,
			Checks:   "A policy set that holds two policies with one ID returns ErrInvalid.",
		},
		{
			Name:     "no-compile-is-diagnostic",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "risk"), pid(fx.acme, "cost-cap")},
			Want:     nil,
			Checks:   "A gate that does not compile against the schema is a diagnostic of each answer, the other gates still match, and the call returns no error.",
		},
		{
			Name:     "request-needs-one-context",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap")},
			Want:     gate.ErrInvalid,
			Checks:   "A request with neither Facts nor Access, or with both, returns ErrInvalid.",
		},
		{
			Name:     "answers-in-request-order",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.webProd, fx.acmeNet},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap")},
			Want:     nil,
			Checks:   "Evaluate of two requests returns two answers in request order, each echoing its request ID.",
		},
		{
			Name:     "access-limit-forbids",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.prod},
			Policies: []gate.PolicyID{pid(fx.prod, "no-agent-destroy")},
			Want:     nil,
			Checks:   "An access limit set with the fixed permit gives the forbid policy as matched for an agent principal, and nothing for a person.",
		},
		{
			Name:     "deterministic",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap")},
			Want:     nil,
			Checks:   "The same set and requests give the same answers, also when the policies come in another order.",
		},
		{
			Name:     "canceled-context",
			Method:   gate.Engine.Evaluate,
			Scopes:   nil,
			Policies: nil,
			Want:     nil,
			Checks:   "Evaluate with a canceled context returns an error that wraps the context error or ErrUnavailable, and no answer.",
		},
		{
			Name:     "external-wrong-answer",
			Method:   gate.Engine.Evaluate,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap"), pid(fx.acme, "unknown")},
			Want:     gate.ErrUnavailable,
			Checks:   "For the external adapter: an answer that does not authenticate, that does not echo the request ID or the set version, or that names acme/unknown, which is not in the set, returns ErrUnavailable.",
		},
	}
}

// engineCheckCases returns the conformance cases of Check and
// CheckLimitMembership.
func engineCheckCases(fx *fixtures) []EngineCase {
	return []EngineCase{
		{
			Name:     "check-accepts-valid",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-cap"), pid(fx.acme, "no-agent-destroy")},
			Want:     nil,
			Checks:   "Check of a valid gate and a valid forbid limit returns no diagnostic and a nil error.",
		},
		{
			Name:     "check-refuses-permit-limit",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "permit-all")},
			Want:     nil,
			Checks:   "Check of an access limit with the effect permit returns one diagnostic for it and a nil error.",
		},
		{
			Name:     "check-refuses-second-policy",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "injection")},
			Want:     nil,
			Checks:   "A condition that closes the clause and starts a second policy gives a diagnostic.",
		},
		{
			Name:     "check-refuses-action",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "names-action")},
			Want:     nil,
			Checks:   "A condition that names action gives a diagnostic.",
		},
		{
			Name:     "check-refuses-unknown-attribute",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "risk")},
			Want:     nil,
			Checks:   "A condition that reads context.risk.score, which the schema does not name, gives a diagnostic.",
		},
		{
			Name:     "check-refuses-has-on-schema-attribute",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "has-cost")},
			Want:     nil,
			Checks:   "A condition with context has cost gives a diagnostic.",
		},
		{
			Name:     "check-refuses-unread-available",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "cost-no-flag")},
			Want:     nil,
			Checks:   "A condition that reads context.cost.deltaMonthly and not context.cost.available gives a diagnostic.",
		},
		{
			Name:     "check-refuses-type-error",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "type-error")},
			Want:     nil,
			Checks:   "A condition that compares context.plan.destroy with a string gives a diagnostic.",
		},
		{
			Name:     "check-refuses-group-of-other-point",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "write-cost")},
			Want:     nil,
			Checks:   "A gate of state.write that reads the group cost, which state.write does not hold, gives a diagnostic.",
		},
		{
			Name:     "check-refuses-unknown-path-set",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.prod},
			Policies: []gate.PolicyID{pid(fx.prod, "unknown-set")},
			Want:     nil,
			Checks:   "A gate that reads paths.touches of a path set that the schema of its scope does not hold gives a diagnostic.",
		},
		{
			Name:     "check-one-diagnostic-each",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "risk"), pid(fx.acme, "type-error"), pid(fx.acme, "cost-cap")},
			Want:     nil,
			Checks:   "Check of two refused policies and one valid policy returns one diagnostic for each refused policy, by policy ID, in the order of the policies.",
		},
		{
			Name:     "check-limit-membership-needs-a-group",
			Method:   gate.Engine.CheckLimitMembership,
			Scopes:   []gate.Scope{fx.acme},
			Policies: []gate.PolicyID{pid(fx.acme, "no-agent-destroy"), pid(fx.acme, "membership-no-group")},
			Want:     gate.ErrLimitMembership,
			Checks:   "CheckLimitMembership returns nil for a limit whose membership test of principal names a group, and an error that wraps ErrLimitMembership for one that names another value.",
		},
		{
			Name:     "check-unusable-schema",
			Method:   gate.Engine.Check,
			Scopes:   []gate.Scope{fx.acme},
			Policies: nil,
			Want:     gate.ErrInvalid,
			Checks:   "Check with a schema version that the engine does not know returns ErrInvalid.",
		},
	}
}
