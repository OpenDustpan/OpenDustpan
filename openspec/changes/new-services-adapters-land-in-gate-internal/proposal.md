---
version: 1
---

Issue: #2186.

## Problem

The gate config service landed in pkg/gate after the split rule, so an embedder sees service code as SDK. The service belongs in the NEW package internal/gatesvc. The ports, types, sentinels and conformance suite stay in pkg/gate.

## Evidence

- ConfigService is defined in pkg/gate on main. Proof: The pack lists type ConfigService at pkg/gate/config.go:69 and the rg hit pkg/gate/config.go:69:type ConfigService.
- Construction of gate.ConfigService outside pkg/gate is in pkg/dustpan. Proof: The rg hits for gate.ConfigService show pkg/dustpan/gateconfig.go:27 gate.NewConfigService and pkg/dustpan/area_installation.go:67.
- The package for the move does not exist yet. Proof: The pack path check gives exists false for internal/gatesvc. This change creates it (NEW).

## Fix boundary

- Add the NEW package internal/gatesvc with ConfigService, checkApprovers, ApproverDirectory and MemberCounts.
- Keep ports, types, sentinels and the suite pkg/gate/approverdirectorytest in pkg/gate. Port types that the port closure reaches stay in pkg/gate.
- pkg/dustpan wires the NEW package internal/gatesvc. NewServer takes no gate service port.
- List the NEW package internal/gatesvc in the depguard domain rule of .golangci.yml.

Out of scope:

- Move of setting.Service, PlacementService, the job Reconciler, forge/github and the chat renderers.
- A change to the gate approver behavior.
- A new lint rule against New*Service in pkg.

## Check

- Add a boundary test that fails on main: the NEW gatesvc package runs the same approver check cases as pkg/gate. Check: go test ./internal/gatesvc/... -run TestApproverCheckSameAfterMove
- Create internal/gatesvc and move ConfigService, checkApprovers, ApproverDirectory and MemberCounts. Rewire pkg/dustpan/gateconfig.go. Check: go build ./... and go test ./pkg/dustpan/...
- List internal/gatesvc in the depguard domain rule of .golangci.yml. Check: golangci-lint run ./internal/gatesvc/...
- Check that no caller keeps a moved name in pkg/dustpan/gateconfig.go and pkg/dustpan/area_installation.go. Check: rg -n 'gate\.(ConfigService|NewConfigService)' pkg/dustpan internal cmd prints nothing
- Wiring: the route GET /api/dustpan/v1/namespaces/{namespace}/gates answers through the NEW service with the same authorization actions. The e2e step calls it with an access token. Confirmed by: CI (make e2e). Check: make e2e
- Docs and usability: no user page changes. Confirmed by: CI (make docs-lint). Check: make docs-lint
- Full check with zero findings. Confirmed by: CI (make check). Check: make check

Depends on:

- `new-services-adapters-land-in-trigger-internal` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ConfigService is defined in pkg/gate. (`pkg/gate/config.go:69 (type ConfigService)`)
- NewConfigService builds it. (`pkg/gate/config.go:94 (func NewConfigService)`)
- ApproverDirectory is defined in pkg/gate. (`pkg/gate/config.go:50 (type ApproverDirectory)`)
- MemberCounts is defined in pkg/gate. (`pkg/gate/config.go:21 (type MemberCounts)`)
- pkg/dustpan builds the service. (`pkg/dustpan/gateconfig.go:19 (func newGateConfigService)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 272: The public SDK surface** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 364 extends decision 4. Chain to the end: ADR 364.
- Current rule: this ADR, with ADR 364.
- Quote: "The service moves to `internal/<domain>svc`."
- Quote: "`pkg/<domain>` keeps the types that the port closure reaches, the sentinel errors, the error types, the ports and the conformance suite."
- Effect: The service moves to the NEW package internal/gatesvc. The suite and the port types stay in pkg/gate.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: evidence path `internal/gatesvc` does not exist in the checkout.

Questions considered and answered:

- Where does the gate ConfigService belong? ADR 272 decision 2: in internal/<domain>svc.
- Does the approver directory suite move? No. ADR 272 decision 2 keeps the conformance suite in pkg/<domain>.
- Do ApproverDirectory and MemberCounts move if the port closure reaches them? ADR 272 decision 4: no exported pkg/ signature names an internal type. A type that the closure reaches stays in pkg/gate.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
