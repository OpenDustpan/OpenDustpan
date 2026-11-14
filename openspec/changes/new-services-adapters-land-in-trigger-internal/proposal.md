---
version: 1
---

Issue: #1839.

## Problem

The problem: the trigger Manager, the dispatcher, the saver check, OutputReadCheck and the jobstarter adapter are service code, and they sit in the public package pkg/trigger. Each exported name there becomes a promise at 1.0. An Embedder who builds a server binary with the SDK needs a small stable pkg surface.

## Evidence

- The trigger service files are in pkg/trigger on main Proof: pkg/trigger/readcheck.go:46 (type OutputReadCheck) and pkg/trigger/saver.go:110 (func NewSaverCheck)
- pkg/trigger/jobstarter is an importable package on main Proof: the pack lists the path pkg/trigger/jobstarter with exists:true
- CheckCycle is a function that stays in pkg/trigger Proof: pkg/trigger/trigger.go:389 (func CheckCycle)
- The trigger manager is wired in pkg/dustpan Proof: pkg/dustpan/triggers.go:125 (func newTriggerManager)

## Fix boundary

- Add the NEW package internal/triggersvc.
- Move the trigger Manager, the dispatcher, the saver check, OutputReadCheck and pkg/trigger/jobstarter into it.
- Keep Trigger, the ports, the sentinels, CheckCycle and triggertest in pkg/trigger.
- Rewire newTriggerManager in pkg/dustpan/triggers.go to the new package.
- Add internal/triggersvc to the domain rule of .golangci.yml.

Out of scope:

- Gate config service and ApproverDirectory move
- Principal NamespaceServiceGuard move
- Notify post move
- Trigger behavior change
- Route, error code and API description changes

## Check

- Add a test in the NEW package internal/triggersvc that builds the manager with NewSaverCheck and NewOutputReadCheck from that package. Check: `go test ./internal/triggersvc/...` fails on main because the package does not exist
- Move the Manager, the dispatcher, the saver check, OutputReadCheck and jobstarter to the NEW package internal/triggersvc. Keep Trigger, the ports, the sentinels, CheckCycle and triggertest in pkg/trigger. The tests of the moved code call the existing route POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire with an access token and pass the same authorization gate as before. Check: `rg -n -F 'NewOutputReadCheck' pkg` and `rg -n -F 'NewSaverCheck' pkg` print nothing
- Rewire newTriggerManager in pkg/dustpan/triggers.go to internal/triggersvc and fix the pkg/dustpan tests. Check: `go test ./pkg/dustpan/...` passes, including TestNewTriggerManagerReadsFinalVersionFromJobStore
- Add internal/triggersvc to the domain rule of .golangci.yml. Check: `golangci-lint run ./...` reports nothing
- Run the trigger e2e step with the real CLI. It calls the existing route POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire with an access token and passes the same authorization gate as before. Docs pages need no change. Confirmed by: CI (make e2e). Check: `make e2e` passes
- Run the full check. The route table and the API description do not change. Confirmed by: the driver's gates. Check: `make check` with zero findings

Depends on:

- `new-services-adapters-land-in-settings-collection-job` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- pkg/trigger holds OutputReadCheck (`pkg/trigger/readcheck.go:46 (type OutputReadCheck)`)
- pkg/trigger holds the saver check constructor (`pkg/trigger/saver.go:110 (func NewSaverCheck)`)
- CheckCycle is a function in pkg/trigger (`pkg/trigger/trigger.go:389 (func CheckCycle)`)
- pkg/dustpan wires the trigger manager (`pkg/dustpan/triggers.go:125 (func newTriggerManager)`)
- A test reads the final version through the wiring (`pkg/dustpan/triggers_wiring_internal_test.go:79 (func TestNewTriggerManagerReadsFinalVersionFromJobStore)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 272: The public SDK surface** (owner-tagged: decision 2).

- Decisions that apply: 2 (current), 4 (extended).
- Changed by: ADR 364 extends decision 4. Chain to the end: ADR 364.
- Current rule: this ADR, with ADR 364.
- Quote: "`pkg/<domain>` keeps the types that the port closure reaches, the sentinel errors, the error types, the ports and the conformance suite."
- Quote: "The service moves to `internal/<domain>svc`."
- Quote: "No exported signature in `pkg/` names a type from `internal/`."
- Effect: The service moves to the NEW package internal/triggersvc. The jobstarter adapter moves with it. The ports, sentinels, CheckCycle and the suite stay.
- Conflict: none.

**ADR 364: Unexported fields and golangci-lint before added checks (owner)** (owner-tagged (all decisions)).

- Decisions that apply: the ADR as a whole.
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Unexported fields and golangci-lint before added checks (owner)"
- Effect: The service types have unexported fields and a constructor. No new tool is added.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does ADR 272 require this move? Yes. Decision 2 keeps ports and sentinels in pkg/trigger and puts the service in internal/<domain>svc.
- Do gate, principal or notify code move here? No. This part is limited to trigger.
- Do exported pkg signatures stay free of internal types (decision 4)? Yes. pkg/dustpan keeps the manager in an unexported field.
- What about the internal/ident import of the Manager? internal/triggersvc may import internal/ident, because it is inside the module tree.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
