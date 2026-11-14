---
version: 1
---

Issue: #961.

## Problem

An operator must pause a bad or unpaid namespace without losing its data. Today the server has no namespace states, so the operator cannot make a namespace read-only or resume it with a record.

## Evidence

- No namespace state field exists on main; suspended matches are unrelated subscription and passkey strings. Proof: rg -n -F --glob '!*_test.go' 'suspended' pkg internal cmd matches 224 with first lines internal/subscriptionsvc/subscriptionsvc.go:37 and internal/mfasvc/service.go:37
- Namespace routes exist on main and will carry the state filter. Proof: GET /api/dustpan/v1/namespaces/{}/collections and ANY/DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/lock in route table

## Fix boundary

- Namespace states active, suspended, disabled and deleted with read and write rules.
- Request filter per state with namespace.suspended refusal.
- NEW operator suspend and resume routes with reason and safe handling of running work.

Out of scope:

- Timed steps with lead notices
- Batched purge after delete
- Billing triggers

## Check

- Add failing tests in `NEW pkg/namespace/namespace_state_test.go` for suspended read pass and write refusal with namespace.suspended. Check: TestSuspendedWriteRefused in `NEW pkg/namespace/namespace_state_test.go` fails on main, passes after.
- Add failing tests in `NEW pkg/namespace/namespace_state_test.go` for NEW operator SuspendNamespace and ResumeNamespace routes with reason and audit entries in both scopes. Check: TestOperatorSuspendResume in `NEW pkg/namespace/namespace_state_test.go` fails on main, passes after.
- Add failing tests in `NEW pkg/namespace/namespace_state_test.go` for running jobs run to end and disabled and deleted refusals. Check: TestRunningWorkSafe in `NEW pkg/namespace/namespace_state_test.go` fails on main, passes after.
- Wire routes, filter per state, and fenced state check. Check: Route test lists NEW suspend and resume routes with installation:suspend-namespace.
- Add docs page for states and operator action. Check: Docs page renders and commands run as written.
- Run usability check and full checks. Check: `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Report helper records scope for unmanaged findings. (`internal/unmanaged/report.go:243 (func RecordScope)`)
- Verifier scopes a namespace value for SSH signatures. (`internal/sshsig/verifier.go:89 (func scopeNamespace)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 215: The namespace is the tenant, and OpenDustpan Cloud is multitenant (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 16 (current), 17 (current).
- Changed by: ADR 248 changes decision 21. Chain to the end: ADR 248, ADR 286.
- Current rule: this ADR, with ADR 248.
- Quote: "Four states (owner). A namespace has one of these states:  | State | From | What works | What the server refuses | | --- | --- | --- | --- | | `active` | creation, or a resume | everything | nothing | | `suspended` | an operator action (A"
- Quote: "The state is in the fenced row. Each write transaction already reads the namespace row for its fence (ADR 4). The same read takes the state, so a write in a `suspended` namespace fails in that transaction. The error code is `namespace.sus"
- Effect: Adds the four state values and the fenced-row state check with the suspended refusal path.
- Conflict: none.

**ADR 216: Namespace administration and operator scope: where each installation action goes (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: ADR 273 changes decision 8. Chain to the end: ADR 273, ADR 286.
- Current rule: this ADR, with ADR 273.
- Quote: "New operator actions. These act on namespaces as objects of the installation. They have operator scope and the operator family: - `installation:suspend-namespace`: sets the state `suspended` or `active` (ADR 215 decision 16). The request takes a reason, and the audit entry of each scope record"
- Effect: Adds the NEW operator suspend and resume routes under installation:suspend-namespace with reason and audit entries in each scope.
- Conflict: none.

**ADR 220: Isolation between namespaces on shared infrastructure (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 10 (current).
- Changed by: ADR 273 changes decision 12; ADR 286 changes; ADR 363 extends decisions 1, 2 and 3. Chain to the end: ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 273, 286, 363.
- Quote: "The state check in the write path. Each write transaction reads the namespace row for its fence (ADR 4). The same read takes the namespace state, so a write in a `suspended` or `disabled` namespace fails in that transact"
- Effect: Reuses the same fenced write-path check for suspended and disabled states.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the state model conflict with an owner-tagged ADR? No. ADR 215 decision 16 defines the four states and ADR 216 decision 4 defines the operator suspend and resume action; this part adds only states, filter and operator routes.
- Should this part add timed transitions or purge? No. Timed steps, lead notices and batched purge are out of scope and move to later parts of the split.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
