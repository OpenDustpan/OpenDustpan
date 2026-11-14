---
version: 2
---

Issue: #1123.

## Why

**Persona: namespace administrator.** A namespace administrator sets approval rules that must hold on every installation. Without this wiring a dev principal could approve on production, or a rule with NoApprover could wait for an approval that nobody can give.

The user path this change must make work:

1. **API.** Set an approval rule with constrained approvers on a namespace, collection, or unit.
2. **API.** Start a plan or apply job that reaches the approval decision point.
3. **API.** Read the gate list of the namespace to find the rule that nobody can meet.
4. **Docs page.** Follow the gates guide to fix approvers and retry the job.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply waits and never reaches quorum because the rule names NoApprover or only dev principals. | Decision denies the unmeetable rule. List the gate approvers and fix them. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |
| Evaluation cannot read the installation kind and fails closed. | Decision denies without counting signatures. Check the server state and retry after the server recovers. | `GET /api/dustpan/v1/maintenance` |

Evidence for the claims above:

- Pure approver helpers live in pkg/gate/approvers.go. Proof: pkg/gate/approvers.go:66:func RuleApprovers(a Approvers) Approvers {
- Service evaluation is not implemented and returns errNotImplemented. Proof: pkg/gate/service.go:257:	return Decision{}, errNotImplemented
- Backup export reads installation kind through backup.KindReader. Proof: pkg/backup/devdata.go:45:type KindReader interface {
- Job gate path already filters signatures with CountsAsSignature. Proof: pkg/job/gate.go:274:	if !policy.CountsAsSignature(a.Subject) {
- The gate list route exists for a namespace. Proof: coverage list: GET /api/dustpan/v1/namespaces/{namespace}/gates (listGates)

## What Changes

- Service.Evaluate reads Production from KindReader once per evaluation and fails closed on error or unknown kind.
- Evaluation normalizes approvers with RuleApprovers, denies Unmeetable requirements, and filters signatures with CountsAsSignature.
- ADR 253 table tests 4a and 4b gain Evaluate-level twins.

Out of scope:

- Full gate evaluation beyond the dev-data approver rules.
- The temp_admins KeepAccessTables restore path, which needs its own RFC.
- New numbered migrations or baseline schema edits.

## Usability

Where the path still costs the person time or surprises them:

- A rule that nobody can meet denies at once instead of waiting for an approval that never arrives.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Service.Evaluate returns errNotImplemented. (`pkg/gate/service.go:257`)
- CountsAsSignature decides whether a subject fills a slot. (`pkg/gate/approvers.go:107`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 253: Dev data does not reach a production installation** (not owner-tagged).

- Decisions that apply: 1 (current), 7 (current), 17 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Its value is `dev` or `production`."
- Quote: "equal to `dev:`."
- Quote: "The package `gate` has the constant `NoApprover`, with the value `!no-approver`."
- Effect: The change applies decision 17 inside Service.Evaluate with the kind source of decision 1 and the dev-subject rule of decision 7. It does not change the strip or refuse layers.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does gate evaluation need the dev-data approver rules or is that a separate RFC? ADR 253 decision 17 owns the NoApprover value. This change wires the pure functions into Service.Evaluate. No RFC is needed.
- What does Evaluate do when KindReader reports an error? It fails closed. The issue scope says a read error fails closed.
- Should this change alter temp_admins handling on restore? No. The issue says it needs an RFC.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
