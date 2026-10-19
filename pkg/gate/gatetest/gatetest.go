// Package gatetest holds the conformance cases of the ports of package
// gate: the store ports (gate.Objects, gate.Settings, gate.StateStore and
// gate.Records) and the policy engine port (gate.Engine).
//
// This version holds the case list as data: the name of each case, the
// port method that it calls, the kinds, scopes and policy IDs that it
// uses, and the behavior that it must check. No adapter exists yet. The
// first store adapter and the first engine adapter add the Run functions
// that execute each case by its name, and an adapter in memory in the
// tests of this package runs them first.
//
// In the tables, the unit web/prod of the tool pulumi is in the collection
// prod of acme, and the unit net of the tool opentofu is in no collection.
package gatetest

import "strings"

// Port names a port of package gate.
type Port string

// The ports that the cases check.
const (
	PortObjects    Port = "Objects"
	PortSettings   Port = "Settings"
	PortStateStore Port = "StateStore"
	PortRecords    Port = "Records"
	PortEngine     Port = "Engine"
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

// Case is one conformance case.
type Case struct {
	// Name is the subtest name. It is unique in its list.
	Name string
	// Port and Method name the port method that the case calls first.
	Port   Port
	Method string
	// Kinds limits a case of PortObjects to these kinds. Empty means each
	// kind.
	Kinds []Kind
	// Scopes holds the scope keys that the case writes or reads.
	Scopes []string
	// Policies holds the policy IDs that the case uses.
	Policies []string
	// Checks is the behavior that the case must check.
	Checks string
}

// StoreCases returns the conformance cases of the store ports.
func StoreCases() []Case {
	return parseCases(storeTable)
}

// EngineCases returns the conformance cases of the policy engine port.
// Each adapter runs each case. The cases whose name starts with
// "external-" run for the adapter of an external service only.
func EngineCases() []Case {
	return parseCases(engineTable)
}

// parseCases reads a table: records separated by a blank line, one
// "field: value" on each line, and a line that starts with a space
// continues the value of the line before it. A line with an unknown field
// gives a case with no name, which the tests of this package refuse.
func parseCases(table string) []Case {
	recs := strings.Split(strings.TrimSpace(table), "\n\n")
	cases := make([]Case, 0, len(recs))

	for _, rec := range recs {
		cases = append(cases, parseCase(rec))
	}

	return cases
}

// parseCase reads one record of a table.
func parseCase(rec string) Case {
	fields := map[string]string{}
	last := ""

	for line := range strings.SplitSeq(rec, "\n") {
		if strings.HasPrefix(line, " ") && last != "" {
			fields[last] += " " + strings.TrimSpace(line)

			continue
		}

		key, value, _ := strings.Cut(line, ": ")
		fields[key] = value
		last = key
	}

	port, method, _ := strings.Cut(fields["calls"], ".")
	c := Case{
		Name: fields["name"], Port: Port(port), Method: method, Kinds: nil,
		Scopes: strings.Fields(fields["scopes"]), Policies: strings.Fields(fields["policies"]),
		Checks: fields["checks"],
	}

	for k := range strings.FieldsSeq(fields["kinds"]) {
		c.Kinds = append(c.Kinds, Kind(k))
	}

	for key := range fields {
		switch key {
		case "name", "calls", "kinds", "scopes", "policies", "checks":
		default:
			c.Name = ""
		}
	}

	return c
}

// storeTable holds the cases of gate.Objects, gate.Settings,
// gate.StateStore and gate.Records.
const storeTable = `
name: put-new-has-version-1
calls: Objects.Put
scopes: namespace/acme
checks: Put with prev 0 of a new ID returns version 1, and Get returns the object with
  version 1.

name: put-new-of-existing-id-conflicts
calls: Objects.Put
scopes: namespace/acme
checks: Put with prev 0 of an ID that the scope holds returns ErrConflict and changes nothing.

name: put-replace-increments-version
calls: Objects.Put
scopes: namespace/acme
checks: Put with prev equal to the stored version replaces the object and returns that version
  plus 1.

name: put-stale-version-conflicts
calls: Objects.Put
scopes: namespace/acme
checks: Put with prev that is not the stored version returns ErrConflict and changes nothing.

name: put-replace-unknown-not-found
calls: Objects.Put
scopes: namespace/acme
checks: Put with prev above 0 of an ID that the scope does not hold returns ErrNotFound.

name: same-id-in-two-scopes
calls: Objects.Put
scopes: unit/acme/opentofu/net unit/globex/opentofu/net
checks: Two scopes each hold an object with one ID, and Get of each scope returns its own
  object.

name: unit-name-with-slash
calls: Objects.Put
scopes: unit/acme/pulumi/web%2Fprod
checks: An object of the unit web/prod is stored and read back with the same scope.

name: get-other-scope-not-found
calls: Objects.Get
scopes: namespace/acme namespace/globex
checks: Get of an ID that only another scope holds returns ErrNotFound.

name: list-own-scope-by-id
calls: Objects.List
scopes: installation namespace/acme collection/acme/prod
checks: List returns the objects of its scope only, ordered by ID, and none of a wider or
  narrower scope.

name: in-orders-by-scope-then-id
calls: Objects.In
scopes: installation namespace/acme collection/acme/prod unit/acme/pulumi/web%2Fprod
checks: In returns the objects of each scope in the order of the scopes, then by ID, and none of
  a scope that is not named.

name: delete-removes
calls: Objects.Delete
scopes: namespace/acme
checks: Delete with the stored version removes the object, and Get then returns ErrNotFound.

name: delete-stale-version-conflicts
calls: Objects.Delete
scopes: namespace/acme
checks: Delete with another version returns ErrConflict and keeps the object.

name: delete-unknown-not-found
calls: Objects.Delete
scopes: namespace/acme
checks: Delete of an ID that the scope does not hold returns ErrNotFound.

name: installation-takes-zero-fence
calls: Objects.Put
scopes: installation
checks: A write at the installation with the zero fence and an entry in audit.ServerScope
  commits and appends the entry.

name: stale-fence-changes-nothing
calls: Objects.Put
scopes: collection/acme/prod
checks: After a bump of the failover version, Put and Delete with the old fence return
  namespace.ErrFenced, change nothing and append no entry.

name: fence-of-other-namespace-refused
calls: Objects.Put
scopes: namespace/acme namespace/globex
checks: A write at a scope of acme with the fence of globex, or with the zero fence, returns
  ErrInvalid and changes nothing.

name: entry-of-other-scope-refused
calls: Objects.Put
scopes: namespace/acme
checks: A write with an audit entry of another scope returns audit.ErrInvalidEntry, changes
  nothing and appends nothing.

name: entry-in-same-transaction
calls: Objects.Put
scopes: namespace/acme
checks: Each committed write appends exactly its entry, and each refused write appends none.

name: scope-without-routes-refused
calls: Objects.Put
scopes: blueprint/web/1.0.0 profile/acme/base
checks: A write at a blueprint version or a profile scope returns ErrInvalid.

name: scope-limit
calls: Objects.Put
scopes: namespace/acme
checks: A new object above the limit of its kind for one scope returns ErrLimit, and a replace
  at the limit commits.

name: final-gate-of-wider-scope-shadow
calls: Objects.Put
kinds: gate
scopes: installation namespace/acme collection/acme/prod unit/acme/pulumi/web%2Fprod
policies: installation/no-destroy collection/acme/prod/no-destroy
checks: A gate with the ID of a final gate of a wider scope returns ErrFinal at each narrower
  scope, also at a unit that the collection of the final gate holds.

name: final-gate-of-unrelated-collection
calls: Objects.Put
kinds: gate
scopes: collection/acme/staging unit/acme/pulumi/web%2Fprod
policies: collection/acme/staging/no-destroy unit/acme/pulumi/web%2Fprod/no-destroy
checks: A final gate of a collection that does not hold the unit does not stop a gate of the
  unit with its ID.

name: non-final-gate-of-wider-scope
calls: Objects.Put
kinds: gate
scopes: namespace/acme unit/acme/pulumi/web%2Fprod
policies: namespace/acme/cost-cap unit/acme/pulumi/web%2Fprod/cost-cap
checks: A gate with the ID of a gate of a wider scope that is not final commits.

name: gate-rule-must-resolve
calls: Objects.Put
kinds: gate
scopes: namespace/acme collection/acme/prod
policies: namespace/acme/dba-review
checks: A gate whose outcome names a rule that its scope and each wider scope do not hold
  returns ErrInvalid. A rule of a narrower scope does not resolve.

name: gate-path-sets-must-resolve
calls: Objects.Put
kinds: gate
scopes: namespace/acme
policies: namespace/acme/network-only
checks: A gate whose Reads names a path set that does not resolve returns ErrInvalid.

name: rule-of-wider-scope-shadow
calls: Objects.Put
kinds: approval-rule
scopes: installation namespace/acme
checks: An approval rule with the ID of a rule of a wider scope that holds the scope returns
  ErrFinal.

name: rule-named-by-gate-delete
calls: Objects.Delete
kinds: approval-rule
scopes: namespace/acme unit/acme/pulumi/web%2Fprod
policies: unit/acme/pulumi/web%2Fprod/dba-review
checks: Delete of a rule that a stored gate names returns ErrInvalid, the error names the policy
  ID of each gate, and the rule stays.

name: path-set-of-wider-scope-shadow
calls: Objects.Put
kinds: path-set
scopes: namespace/acme collection/acme/prod
checks: A path set with the ID of a path set of a wider scope that holds the scope returns
  ErrInvalid.

name: path-set-read-delete
calls: Objects.Delete
kinds: path-set
scopes: namespace/acme
policies: namespace/acme/network-only
checks: Delete of a path set that a stored gate, approval default or ownership rule reads
  returns ErrInvalid, names each reader, and the path set stays.

name: ownership-path-sets-must-resolve
calls: Objects.Put
kinds: ownership-rule
scopes: collection/acme/prod
checks: An ownership rule whose Reads names a path set that does not resolve returns ErrInvalid.

name: concurrent-shadow
calls: Objects.Put
kinds: gate
scopes: namespace/acme unit/acme/pulumi/web%2Fprod
policies: namespace/acme/no-destroy unit/acme/pulumi/web%2Fprod/no-destroy
checks: A final gate of acme and a gate of web/prod with the same ID, saved at the same time,
  never both commit.

name: approval-default-none
calls: Settings.ApprovalDefault
scopes: namespace/acme
checks: A scope with no approval default returns ErrNotFound.

name: approval-default-round-trip
calls: Settings.PutApprovalDefault
scopes: namespace/acme collection/acme/prod unit/acme/pulumi/web%2Fprod
checks: PutApprovalDefault stores each field, follows the version rules of Objects.Put, and
  appends its entry.

name: approval-default-not-at-installation
calls: Settings.PutApprovalDefault
scopes: installation
checks: PutApprovalDefault at the installation returns ErrInvalid.

name: approval-default-path-sets-must-resolve
calls: Settings.PutApprovalDefault
scopes: collection/acme/prod
checks: An approval default whose Reads names a path set that does not resolve returns
  ErrInvalid.

name: carry-over-default-off
calls: Settings.CarryOver
scopes: installation namespace/acme
checks: A scope with no stored setting returns off with version 0.

name: carry-over-round-trip
calls: Settings.PutCarryOver
scopes: namespace/acme collection/acme/prod unit/acme/pulumi/web%2Fprod
checks: PutCarryOver stores on, off and forbidden with the version rules of Objects.Put.

name: carry-over-on-at-installation
calls: Settings.PutCarryOver
scopes: installation
checks: PutCarryOver of on at the installation returns ErrInvalid, and forbidden commits.

name: settings-fence
calls: Settings.PutCarryOver
scopes: collection/acme/prod
checks: A setting write with a stale fence returns namespace.ErrFenced and changes nothing.

name: set-state-keeps-version
calls: StateStore.SetState
scopes: namespace/acme
policies: namespace/acme/cost-cap
checks: SetState to invalid keeps the version, Get returns the state, and no audit entry is
  appended.

name: set-state-stale-version
calls: StateStore.SetState
scopes: namespace/acme
policies: namespace/acme/cost-cap
checks: SetState with another version returns ErrConflict, and with an unknown gate ErrNotFound.

name: save-clears-invalid
calls: Objects.Put
kinds: gate
scopes: namespace/acme
policies: namespace/acme/cost-cap
checks: After SetState to invalid, a Put of the gate by a person stores the state that Put
  names, so a save clears invalid.

name: record-round-trip
calls: Records.Put
scopes: namespace/acme unit/acme/pulumi/web%2Fprod
policies: installation/builtin/plan-readable unit/acme/pulumi/web%2Fprod/cost-cap
checks: Put and Get keep each field: the fact document of each unit, the path set versions, each
  policy result, the requirements with carried signatures, the fixed controls and the
  result.

name: record-other-namespace-not-found
calls: Records.Get
scopes: namespace/acme namespace/globex
checks: Get of a record of acme with the namespace globex returns ErrNotFound.

name: record-used-id
calls: Records.Put
scopes: namespace/acme
checks: Put of a used record ID returns ErrConflict and keeps the first record.

name: record-fence
calls: Records.Put
scopes: namespace/acme namespace/globex
checks: Put with a stale fence returns namespace.ErrFenced, and with the fence of another
  namespace ErrInvalid.

name: record-of-installation-object
calls: Records.NoteRefusal
scopes: installation
policies: installation/no-agent-tokens
checks: A refusal of an access limit on an installation object is stored with the zero fence and
  read with the empty namespace.

name: list-by-change-oldest-first
calls: Records.ListByChange
scopes: namespace/acme
checks: ListByChange returns the records of one change, oldest first, and none of another
  change.

name: query-window-and-limit
calls: Records.Query
scopes: namespace/acme
checks: Query returns the records of its point with At in [From, To), newest first, at most
  Limit, and returns ErrInvalid for a limit of 0 or above 1000.

name: note-refusal-coalesces
calls: Records.NoteRefusal
scopes: namespace/acme
policies: namespace/acme/no-agent-secrets
checks: A second refusal of the same subject, action and limit within the window returns the
  first ID, adds 1 to Count and sets LastAt.

name: note-refusal-new-record
calls: Records.NoteRefusal
scopes: namespace/acme
policies: namespace/acme/no-agent-secrets namespace/acme/no-destroy
checks: A refusal after the window, of another subject, action or limit, stores a new record
  with Count 1.
`

// engineTable holds the cases of gate.Engine.
const engineTable = `
name: empty-set-matches-nothing
calls: Engine.Evaluate
checks: An empty policy set gives one answer for each request, with no match and no diagnostic.

name: condition-matches
calls: Engine.Evaluate
scopes: namespace/acme
policies: namespace/acme/cost-cap
checks: A gate with context.cost.available && context.cost.deltaMonthly > 10000 matches a
  document with a delta of 15000 and not one of 5000.

name: empty-condition-is-true
calls: Engine.Evaluate
scopes: namespace/acme
policies: namespace/acme/always
checks: A gate with an empty condition matches each request of its decision point.

name: unless-exempts
calls: Engine.Evaluate
scopes: namespace/acme
policies: namespace/acme/no-destroy
checks: A gate that matches does not match when its unless is true.

name: action-is-pinned
calls: Engine.Evaluate
scopes: namespace/acme
policies: namespace/acme/cost-cap
checks: A gate of change.planned never matches a request of change.apply or state.write.

name: matched-ids-keep-scope
calls: Engine.Evaluate
scopes: installation namespace/acme unit/acme/pulumi/web%2Fprod
policies: installation/cost-cap namespace/acme/cost-cap unit/acme/pulumi/web%2Fprod/cost-cap
checks: Three gates with one ID in three scopes that each match are three distinct matched
  policy IDs, equal to the IDs of the set.

name: builtin-id
calls: Engine.Evaluate
scopes: installation
policies: installation/builtin/plan-readable
checks: A built-in gate matches with its policy ID installation/builtin/plan-readable.

name: principal-groups-are-parents
calls: Engine.Evaluate
scopes: namespace/acme
policies: namespace/acme/sre-only
checks: A gate with unless { principal in OpenDustpan::Group::"sre" } does not match a principal
  with the group sre.

name: path-set-attributes
calls: Engine.Evaluate
scopes: collection/acme/prod
policies: collection/acme/prod/network-only
checks: A gate that reads context.paths.available && context.paths.only["network"] matches when
  Go gave only true for the path set network.

name: error-is-diagnostic
calls: Engine.Evaluate
scopes: namespace/acme
policies: namespace/acme/overflow namespace/acme/cost-cap
checks: A gate that gives an evaluation error, for example an overflow, is a diagnostic and not
  a match, the other gates still match, and the call returns no error.

name: answers-in-request-order
calls: Engine.Evaluate
scopes: unit/acme/pulumi/web%2Fprod unit/acme/opentofu/net
policies: namespace/acme/cost-cap
checks: Evaluate of two requests returns two answers in request order, each echoing its request
  ID.

name: access-limit-forbids
calls: Engine.Evaluate
scopes: collection/acme/prod
policies: collection/acme/prod/no-agent-destroy
checks: An access limit set with the fixed permit gives the forbid policy as matched for an
  agent principal, and nothing for a person.

name: deterministic
calls: Engine.Evaluate
scopes: namespace/acme
policies: namespace/acme/cost-cap
checks: The same set and requests give the same answers, also when the policies come in another
  order.

name: canceled-context
calls: Engine.Evaluate
checks: Evaluate with a canceled context returns an error that wraps the context error or
  ErrUnavailable, and no answer.

name: external-wrong-answer
calls: Engine.Evaluate
scopes: namespace/acme
policies: namespace/acme/cost-cap namespace/acme/unknown
checks: For the external adapter: an answer that does not authenticate, that does not echo the
  request ID or the set version, or that names namespace/acme/unknown, which is not in the
  set, returns ErrUnavailable.

name: check-accepts-valid
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/cost-cap namespace/acme/no-agent-destroy
checks: Check of a valid gate and a valid forbid limit returns nil.

name: check-refuses-permit-limit
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/permit-all
checks: Check of an access limit with the effect permit returns a *CheckError that wraps
  ErrInvalid.

name: check-refuses-second-policy
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/injection
checks: A condition that closes the clause and starts a second policy is refused.

name: check-refuses-action
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/names-action
checks: A condition that names action is refused.

name: check-refuses-unknown-attribute
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/risk
checks: A condition that reads context.risk.score, which the schema does not name, is refused.

name: check-refuses-has-on-schema-attribute
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/has-cost
checks: A condition with context has cost is refused.

name: check-refuses-unread-available
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/cost-no-flag
checks: A condition that reads context.cost.deltaMonthly and not context.cost.available is
  refused.

name: check-refuses-type-error
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/type-error
checks: A condition that compares context.plan.destroy with a string is refused.

name: check-refuses-group-of-other-point
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/write-cost
checks: A gate of state.write that reads the group cost, which state.write does not hold, is
  refused.

name: check-refuses-unknown-path-set
calls: Engine.Check
scopes: collection/acme/prod
policies: collection/acme/prod/unknown-set
checks: A gate that reads paths.touches of a path set that the schema of its scope does not hold
  is refused.

name: check-one-diagnostic-each
calls: Engine.Check
scopes: namespace/acme
policies: namespace/acme/risk namespace/acme/type-error namespace/acme/cost-cap
checks: Check of two refused policies and one valid policy returns a *CheckError with one
  diagnostic for each refused policy, by policy ID.
`
