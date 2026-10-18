# Spike: one policy model (not for merge)

This spike tests one model for the many ways to say "no" or "ask someone
first". It is code in `pkg/gate` (the model), `internal/api/gate` (one route)
and tests. It changes no ADR. It adds `github.com/cedar-policy/cedar-go`
v1.8.0 (released 2026-06-01) to `go.mod`.

## What the spike built

- **Gates.** A stored rule at a decision point. The condition is the body of
  one Cedar `permit ... when { }` policy over a typed fact document. The
  outcome is data: `deny`, `approval:<rule>` or `warn`. The mode is `enforce`
  or `evaluate`. The gate ID is the Cedar policy ID.
- **Access limits.** One Cedar `forbid` policy for each limit, evaluated after
  the grants allow, with the fixed `permit (principal, action, resource);`.
  A `permit` in a limit set is refused at save.
- **Approval rules.** Data only: groups, users, count, `authorMayApprove`
  (default false), `freshAuth`.
- **Facts.** Fact schema v0 of `change.planned`: plan counts, cost
  (`deltaMonthly` in cents, `available`), changed paths, input keys, unit
  collection and labels. The cost comes from a JSON file.
- **Owner rules.** The server stores them (managed as code through the IaC
  providers). The spike has no repository file: the first push had an
  `OWNERS.dustpan` reader and it is removed. The changed paths are a fact; the
  test reads them with `git diff --name-only` between two commits.
- **Decision record.** One JSON record for each evaluation, also for an access
  limit and for an error. A route reads it.
- **Offline runner.** `gate.RunTestFile` runs a JSON file of gates and cases
  without a server.

## What was not built

- The harness drives the steps in Go (`TestSpikeEndToEnd`). It does not run the
  job or change-order path, and it adds no spike flag, so the default build is
  unchanged. The wiring into `pkg/job` is larger than a spike.
- Real store: the SQLite store holds the namespace and the audit chain. The
  audit entry of the apply names the record ID in `RequestID` and in a
  change. There is no state version in the test: the workspace state path was
  not wired, so "state version names the record ID" is not shown.
- Records, gates, limits, rules and approvals are in memory. Group
  membership of a person is an input of the call.
- The routes are not mounted in the server and have no authorization action.
  The read handler takes an `Authorize` function and answers 404 on a
  refusal. The configuration handlers take an `AuthorizeConfig` function.
- The cost fact comes from a file and not from `pkg/cost`.

## Owner decisions applied after the first push

1. **Approval defaults.** `ApprovalDefault` is set at a namespace, a collection
   or a unit (the installation is refused). The strictest wins: the highest
   count and the smallest fresh window. With none set, the default is off and
   nothing adds a minimum of one approval (`TestApprovalDefaults`). A default
   is one requirement named `default` that any person other than the author
   can meet; who may approve a default is not decided.
2. **Exemptions.** A gate and an approval default have an optional `unless`
   Cedar body. The wider rule carves out its own exemptions. No outcome
   relaxes another (`TestUnlessExemption`, and the `unless` of the collection
   default in `TestApprovalDefaults`).
3. **Stored in the server.** Gates, approval rules, approval defaults and owner
   rules are objects with `PUT` routes in `internal/api/gate/config.go`. The
   body is parsed and type-checked at save, with an unknown field refused,
   a scope check, and 422 for an invalid object, 409 for a final gate.
4. **Ownership rules take conditions** (shape below).
5. **Fresh authentication.** `freshAuthSeconds` is nil (none), 0 (a new
   authentication at each approval) or a window. The approver gives
   `reauthenticatedNow` and `authAgeSeconds` (`TestFreshAuthWindow`).
6. **A warn blocks auto-apply.** The record has `autoApply`: it is true when the
   result is `allow` and no enforced gate warns. A warn in `evaluate` mode
   does not block (`TestWarnBlocksAutoApply`).

### Later decisions

- **Ownership rules are server-only.** The reader of `OWNERS.dustpan` and its
  tests are removed; the end-to-end test stores the rule through the registry.
- **Access limits** stay Cedar `forbid` only, for each scope.
- **Missing fact: a typed value, and templates fail closed.** A fact always
  has a value of its type (`cost.available` is false and `deltaMonthly` is 0
  when there is no cost). A gate message may hold `{{fact.path}}`. A path that
  is not a fact is refused at save (`ErrInvalid`). A template that cannot
  render at run time stops the change with `ErrRuleFailed` and does not give
  an empty string (`TestMessageTemplateFailsClosed`, step 3 of the
  end-to-end test).
- **Names.** The stored rule is a "gate". The job state that waits for people
  is "approval wait": the result `require-approval` of a record is the cause
  of that state, and the spike does not add the job state.

### Proposed shape of an ownership rule

The server stores the rule and no repository file holds it. The shape is:

```json
{
  "id": "net-cidr",
  "scope": {"level": "collection", "name": "acme/prod"},
  "paths": ["modules/network/**"],
  "when": "context.change.inputKeys.contains(\"cidr\") && context.plan.replace == 0",
  "groups": ["netops"], "users": [], "count": 2,
  "freshAuthSeconds": 0
}
```

- `paths` is matched in Go, because Cedar has no quantifier over a set. Empty
  means any change.
- `when` is a Cedar condition over the typed facts: operation kind, plan
  counts, changed input keys, cost, labels and the principal. It is one
  policy in the same Cedar request as the gates, so it is type-checked at
  save with the same schema. Empty means true.
- A rule applies when the gate with the outcome `approval:owners` matches, the
  paths match and `when` is true. Each applying rule is one requirement named
  `owners/<id>`; the record shows its need, its count so far and who may
  approve.
- Open: today a rule does nothing when no gate names `owners`. If a rule must
  apply alone, give it its own `enforce` outcome instead of a gate.
- Open: richer approvers (a group, or the owners of a path in a tool-neutral
  inventory) and a rule that needs approvals from two named groups
  (`all of`) are not in the spike. A list of requirements per rule covers
  `all of`.

## Results of the plan steps

All nine steps pass in `go test -race ./pkg/gate`. Step 1 allows with no match.
Steps 2 and 3 give `require-approval` with matches `acme/cost-warn` and
`acme/net-owners` and one requirement `owners/network` (need 1). Step 4:
alice gets `ErrAuthorApproval`, carol gets `ErrNotApprover` that names
`owners/network`, bob is accepted. Step 5: the apply writes the audit entry
and the chain checks as valid. Step 6: carol gets `ErrLimited` that names
`acme/no-prod-destroy`, and a member of `sre` passes. Step 7: the match of an
`evaluate` gate is in the record and the result stays `allow`. Step 8: a gate
that reads `context.risk.score` gives `ErrRuleFailed`, the record has result
`error` and lists the Cedar message. Step 9: `testdata/pass.gatetest.json`
passes and `testdata/fail.gatetest.json` has one failed case.

## What worked

- One Cedar request for each unit, one `permit` for each gate with the gate ID
  as policy ID. The determining policies of cedar-go are the matched gates.
- Parse at save finds a closing brace in a condition: a condition that makes a
  second policy is refused (`TestSaveRefusals`).
- A runtime error is a diagnostic of cedar-go, so the "never allow, never
  deny" rule needs about ten lines.
- Fact documents convert to Cedar records with the JSON form of cedar-go. No
  hand-written mapping.
- Set order does not matter: `context.change.inputKeys == ["cidr", "region"]`
  is true for any order.
- A subset test works with the operands turned around:
  `["cidr","region","zone"].containsAll(context.change.inputKeys)`.

## What did not fit Cedar

- **No quantifier.** "Every changed path is under `modules/`" cannot be said.
  `.all(p, ...)` is a parse error. Only `contains`, `containsAll` and
  `containsAny` were the set tests that the spike used. A glob works on one string
  (`like`), never on each element of a set. An owner rule does the path
  matching in Go for this reason.
- **No open map.** A record has the keys that the schema names. Unit labels are
  a `Set<String>` of `key=value` strings.
- **`has` fails open.** `context has risk && context.risk.score > 3` passes the
  type check when `risk` is not in the schema, and the gate never matches. The
  gate stops without a message. The same condition without `has` gives
  `ErrRuleFailed`. A save rule must refuse `has` on an attribute that is not
  in the schema, or on any attribute of the required facts.

## Does the schema validator type-check at save?

Yes, with limits. `x/exp/schema/validate` of cedar-go v1.8.0 checks a policy
against `FactSchema` (`schema.go`). It found the unknown fact
`context.risk.score`, the unknown attribute `context.cost.nope` and the type
error `deltaMonthly > "high"` at save, with a message that names the policy.

- The package is under `x/exp`: its README says there is no compatibility
  promise. A pin to one version and a test of the cases above limit the risk.
- A policy with `action` unconstrained is checked against every action in the
  schema. A gate failed because `unit:run-destroy` has no `cost` in its
  context. A gate must carry `action == Action::"change.planned"`. The spike
  adds this to the policy text, so the decision point is the Cedar action.
- The `has` hole above.

## Measured cost of one gate evaluation for one unit

`BenchmarkGateEvaluation`, `go test -bench Gate -benchtime 20000x`, 4 CPUs of
an Intel Xeon at 2.1 GHz, Go 1.27.2. Three repeats of the 3-gate case gave
97 to 102 µs in the first, single-case benchmark. The table is one run of the
final benchmark:

| Gates | Time for one `Planned` call | Allocations |
| ----- | --------------------------- | ----------- |
| 0     | 61 µs                       | 331         |
| 1     | 78 µs                       | 412         |
| 3     | 98 µs                       | 577         |

A call includes: the parse of each gate (it is not cached), the fact document,
the entities, the Cedar request, the combine and the digest of the input. The
call with no gate is the fixed cost. In this benchmark each gate adds 6 to
17 µs; two runs of the same code gave 65 and 61 µs for 0 gates, so a single
run has a spread of a few µs. The benchmark does not cover a store read of the gates, the write of
the record, or a plan with many resources.

## What the design must change

1. **Pin the action in every gate policy** and type-check at save against the
   fact schema of the decision point. Without the pin, the check fails for a
   wrong reason.
2. **Forbid `has` on schema attributes** at save, or allow it only on facts that
   the schema marks optional. Optional facts need an `available` flag as the
   cost fact has.
3. **Cache compiled policy sets** by gate ID and version. The spike parses at
   each call. The version digest is already in the record.
4. **A bad gate stops every change below its scope.** A gate at the
   installation scope with a runtime error stops all units. Save-time type
   check and `gates test` are required, not optional. Consider a break-glass
   action for `ErrRuleFailed`; this spike does not decide it.
5. **Owner rules need a gate.** The spike makes `approval:owners` the way in.
   A rule or a file section does nothing when no gate names `owners`. See the
   open points of the proposed shape.
6. **Approval assignment is greedy.** A person counts for the first open
   requirement they can meet. A fair assignment of people to requirements is a
   matching problem. It matters only for overlapping groups.
7. **Final gates.** The spike only stops a re-scope of a final gate by ID. A
   narrower scope cannot replace a wider gate because gate IDs are unique in
   the installation. The model needs a decision: can a narrower scope switch a
   wider gate to `evaluate`?
8. **Dependency.** `cedar-go` v1.8.0 brings `golang.org/x/exp` at a 2022
   pseudo-version. `govulncheck` reports nothing for the new packages. `make
   licenses`, `make semgrep`, `make trivy` and the Cedar service port were not
   run or built.
9. **Untouched questions.** State version naming, persistence of records, the
   job-path wiring, the authorization action of the record route and the
   mapping to the existing `pkg/policy` Cedar contract are open.

## Checks run

- `go test -race ./pkg/gate ./internal/api/gate`: pass.
- `make nilaway` equivalent on the two packages: no finding.
- `govulncheck` on the two packages: no vulnerability.
- `golangci-lint run` on the two packages: **the findings remain (89 on the last count)**. They are
  54 `exhaustruct_v5` on literals of test fixtures and value types, `cyclop`,
  `gocognit` and `maintidx` on the scenario tests, 8 `funcorder`, 7 `gocritic`
  `rangeValCopy`, 2 `gochecknoglobals` in tests, and smaller ones. The spike does not fix them, so it does not meet the definition of
  done. The full `make check` was not run.
