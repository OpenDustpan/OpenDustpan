Issue: #3107.

## Problem

A role that holds an action without a family is refused on every grant that
is not on the installation, and nothing tells the author of a new action that
the family table misses it. The planner role failed this way: the five job
actions of `pkg/identity` had no entry in the family table of
`pkg/access/access.go`, so a grant of the planner role on a collection answered
400. Only an e2e step found it. Any new action can miss the table in the same
way. Some actions stay without a family on purpose, until their routes land.

## Evidence

- **The family table.** `pkg/access/access.go:115`
  `var families = map[identity.Action]Family{`, and `:229`
  `func FamilyOf(action identity.Action) (Family, bool) {` answers false for an
  action that the table does not hold.
- **The refusal.** `pkg/access/service.go:1040-1043`: for a grant on a
  namespace, a collection or a unit, `actionOutside(role, FamilyData)` refuses
  with `role %q holds %s, which only a grant on the installation can give`.
- **The deliberate gaps.** `pkg/access/access_test.go:490`: "features with no
  routes yet stay unclassified. So no role holds them". The test
  `TestActionsWithoutRoutesAreUnclassified` at `:493` lists those actions.
- **The actions.** `pkg/identity/identity.go:156` `type Action string`; the
  block at `:158-651` ("Actions known to the core. Adapters may define their
  own.") holds 125 constants (`grep -c 'Action = "' pkg/identity/identity.go`
  gives 125). Go cannot list the constants of a block at run time, so no test
  can compare the family table with them today without reading the source.
- **The uses of an action in a constant context.** `rg -n "const .*identity\.Action"`
  gives three: `pkg/trigger/saver.go:32`, `:35` and
  `internal/agent/actions.go:66`. `rg -n "case identity\.Action"` gives two
  `case` lines in `internal/api/server/githost_fixture_test.go:60` and `:64`;
  a `case` may name a variable.

## ADR check

- ADR 216 (owner), context: "`pkg/access/access.go` gives each action one
  family: operator, administration, data, delegation or self." This change
  keeps the families and adds no family.
- ADR 166, "The new actions have no family": "`access.FamilyOf` answers no
  family for them, and a test holds that 'until its routes land'. So no
  built-in role holds them." This change keeps that list and makes it the one
  explicit place for an action without a family.
- The catalog of code rules, `docs/reference/code-rules.md:13`: "an action
  that no family classifies is not sensitive. A check that must fail closed on
  an unknown action reads the family itself". This change does not change the
  rule.
- ADR 364 (owner): "Prefer unexported fields with a constructor that checks
  the invariants ... Do not add a custom check or a new tool." The catalog is
  complete by construction (an unexported registry that only the declaration
  of an action fills), not by a scan of the source.
- ADR 272 decision 4: `pkg/identity` is SDK. The change adds one exported
  function, `identity.Actions()`, which returns a copy. No tag of OpenDustpan
  exists, so the SDK surface may change (ADR 73).
- `rg -n "family" docs/adr` gives no decision that a core action must stay
  without a catalog.

## Fix boundary

- `pkg/identity`: each core action is declared through one unexported
  function that records it in an unexported registry:
  `ActionNamespaceRead = define("namespace:read")`. The actions become
  package variables of type `Action`, so the registry is complete by
  construction. `identity.Actions() []Action` returns a sorted copy. `define`
  panics on a value that is empty, not of the form `area:verb`, or recorded
  twice; the package tests prove each case.
- The three constant declarations that name an action
  (`pkg/trigger/saver.go:32`, `:35`, `internal/agent/actions.go:66`) become
  variables.
- `pkg/access`: a test walks `identity.Actions()` and fails for an action
  that is neither in the family table (`access.FamilyOf`) nor on the list of
  `TestActionsWithoutRoutesAreUnclassified`, which moves to one variable in
  the test file so that both tests read it. It also fails for
  an action that is on that list and has a family.
- No change of a family, a role, a route or a refusal.

## Check

`go test ./pkg/identity/... ./pkg/access/...` passes; removing one job action
from the family table makes the new test fail and name it.

Open questions: none. Considered:
- Constants or variables? Variables: Go cannot enumerate constants, and a
  registry filled by the declaration is the structure that makes a missing
  entry impossible (ADR 364). Five uses in constant or `case` positions work
  as variables (the evidence above lists them).
- Adapter actions? The comment at `pkg/identity/identity.go:158` lets adapters
  define their own; they are not core actions and stay out of the registry.
- Where does the unclassified list live? In the test file of `pkg/access`,
  next to `TestActionsWithoutRoutesAreUnclassified`, as today; a feature moves
  its actions off the list when its routes land.
