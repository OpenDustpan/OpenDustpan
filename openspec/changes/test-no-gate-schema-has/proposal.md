---
version: 1
---

Issue: #2995.

## Problem

A gate condition rule assumes no gate schema exposes a group reference. If a schema gains a Group-typed attribute, a policy could reach a group that the group check does not see.

## Evidence

- Main already checks group literals in gate conditions. Proof: pkg/gate/cedargo/groups.go:367 (func holdsGroupLiteral)
- Gate engine conformance already covers refusal of has on a schema attribute. Proof: pkg/gate/gatetest/run_engine.go:660 (func runCheckRefusesHasOnSchemaAttribute)
- Gate engine conformance covers refusal of a group of another decision point. Proof: pkg/gate/gatetest/run_engine.go:693 (func runCheckRefusesGroupOfOtherPoint)
- The issue asks for a test that fails on Group, Set of Group, or an entity parent of type Group and names the dependent rule. Proof: Issue #2995 Change paragraph

## Fix boundary

- Add a test that loads each gate schema and fails when any entity type or context has an attribute of type Group, Set of Group, or an entity type that reaches Group through its parent chain, and names the dependent group-literal rule in the failure message.

Out of scope:

- Change to gate condition checks
- Change to gate schemas or context fields

## Check

- Add NEW pkg/gate/schema_guard_test.go with TestNoGateSchemaHasGroupAttribute that loads each gate schema and asserts no Group-typed attribute. Check: go test ./pkg/gate -run TestNoGateSchemaHasGroupAttribute -count=1 passes on current schemas
- Add a permanent table test in NEW pkg/gate/schema_guard_test.go with in-memory schemas for Group, Set of Group, nested record, union, common type, parent chain, and one clean schema. Check: go test ./pkg/gate -run TestSchemaGuardTable -count=1 passes with permanent in-memory schemas
- Assert in NEW pkg/gate/schema_guard_test.go that the loaded schema set equals the explicit expected ID list stored in the test. Check: go test ./pkg/gate -run TestNoGateSchemaHasGroupAttribute -count=1 fails after adding an ID to the expected list test double
- Run the gate suite for the test-only change. Check: go test -race ./pkg/gate/... passes with zero failures

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main already checks group literals in gate conditions. (`pkg/gate/cedargo/groups.go:367 (func holdsGroupLiteral)`)
- Main already tests refusal of has on a schema attribute. (`pkg/gate/gatetest/run_engine.go:660 (func runCheckRefusesHasOnSchemaAttribute)`)
- Main already tests refusal of a group of another decision point. (`pkg/gate/gatetest/run_engine.go:693 (func runCheckRefusesGroupOfOtherPoint)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change need a gate route or schema change? No. The issue asks only for a guard test over gate schemas that main already loads. No route, schema, or condition logic changes.
- What must the new test assert? The guard loads every gate schema through the existing pkg/gate schema source, walks every entity attribute and every context field including nested records, sets, unions, resolved common types, and parent chains, and fails on type Group, Set of Group, or an entity type that reaches Group through a parent chain. The failure message names the group-literal rule that holdsGroupLiteral enforces.
- How does the guard stay complete when a new schema is added? The NEW test stores an explicit expected list of schema IDs and fails when the loaded set differs, so a new schema fails the guard until the list is reviewed and updated.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
