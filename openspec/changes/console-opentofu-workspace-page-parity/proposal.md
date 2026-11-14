---
version: 1
---

Issue: #994.

## Problem

A developer who runs OpenTofu needs a workspace page with state versions, the lock holder and force-unlock, and an on-call person needs to act from a phone. The server part verifies those reads and the force-unlock action with a reason, so the console page in its own repository can use them.

## Evidence

- TFE-compatible routes for workspaces, state versions, lock, unlock and force-unlock exist. Proof: The pack lists ANY /api/v2/workspaces/{}, ANY /api/v2/workspaces/{}/actions/lock, ANY /api/v2/workspaces/{}/actions/force-unlock and ANY /api/v2/workspaces/{}/state-versions.
- Handlers for the workspace page, state versions and lock holder exist. Proof: internal/api/tfe/workspaces.go:459 (func Handler.listWorkspacePage), internal/api/tfe/states.go:691 (func Handler.listStateVersions), internal/api/tfe/locks.go:85 (func Handler.lockHolder).
- A manual-lock ForceUnlock exists. Proof: pkg/manuallock/service.go:168 (func Service.ForceUnlock).

## Fix boundary

- Audit G1 to G12 against the coverage matrix and list the result in the issue; file one issue per open gap.
- Keep unit:write-forced on the TFE-compatible and http-backend force-unlock routes.
- Add the NEW required reason of 1 to 500 characters on the console force-unlock route.
- Ship the docs page and the console-repository handoff.

Out of scope:

- The console workspace page markup, on-call views at phone width and axe gates: console repository.
- New server routes beyond the NEW reason field on force-unlock.

## Check

- Write the failing test for the maintainer 403 on force-unlock in NEW internal/api/tfe/force_unlock_test.go. Check: TestForceUnlockMaintainer403 calls ANY /api/v2/workspaces/{}/actions/force-unlock with an access token and expects 403
- Write the failing test for the missing reason on the console route in NEW internal/api/inventory/force_unlock_reason_test.go. Check: TestForceUnlockReasonRequired calls ANY /api/v2/workspaces/{}/actions/force-unlock with an access token and expects 422
- Audit G1 to G12 against the coverage matrix in internal/api/tfe/workspaces.go:459 (func Handler.listWorkspacePage) and open one issue per open gap. Check: No route: the test compares the coverage matrix against the issue list only and writes the result.
- Enforce the NEW reason and the audit entry in pkg/manuallock/service.go:168 (func Service.ForceUnlock). Check: TestForceUnlockReasonRequired calls ANY /api/v2/workspaces/{}/actions/force-unlock with an access token
- Add the e2e step for force-unlock across the OpenTofu matrix with the real CLI in e2e/tool.go:110 (func OpenTofu). Confirmed by: CI (e2e job) Check: `make e2e` drives tofu force-unlock on ANY /api/v2/workspaces/{}/actions/force-unlock with an access token across the OpenTofu matrix. Confirmed by: CI (e2e job)
- Add the docs page for the workspace reads and force-unlock. Check: No route: the test reads the new docs page only
- Check the handoff for phone-width on-call use with pkg/dustpan/console_control_test.go:14. Check: No route: the test follows the docs page and checks each error names one command only
- Run make check. Confirmed by: CI (make check) Check: `make check` reports zero findings. Confirmed by: CI (make check)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The TFE handler lists state versions. (`internal/api/tfe/states.go:691 (func Handler.listStateVersions)`)
- The TFE handler resolves the lock holder. (`internal/api/tfe/locks.go:85 (func Handler.lockHolder)`)
- The inventory handler has a force-unlock path. (`internal/api/inventory/locks.go:256 (func Handler.forceUnlock)`)
- The manual-lock service has ForceUnlock. (`pkg/manuallock/service.go:168 (func Service.ForceUnlock)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 239: A force-unlock needs `unit:write-forced` on every OpenTofu route** (not owner-tagged).

- Decisions that apply: 1 (current), 4 (current), 5 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "One action for each force-unlock (product manager). A force-unlock needs `unit:write-forced` on the workspace unit, on the TFE-compatible API and on the http backend."
- Quote: "`can-force-unlock` follows the same action. The attribute `permissions.can-force-unlock` of the workspace document is true only when the caller has `unit:write-forced` on the unit."
- Effect: The change keeps unit:write-forced on force-unlock and the can-force-unlock flag.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "No page, not done. A reviewer refuses such a pull request when the page is not in it. A later pull request for the docs does not make the first one done."
- Effect: The docs page ships in the same pull request.
- Conflict: none.

**ADR 236: Accessibility gates in the console checks and for every language** (owner-tagged: decision 2).

- Decisions that apply: 2 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The e2e gate covers every direction and script. The axe gate of ADR 183 decision 3 runs in `en`, `en-XA` and `ar-XB` on each change."
- Effect: Axe gates run in the console repository, this change hands off the page.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this change need a new server route? No. The routes exist on main. ADR 239 decisions 1 and 6 name the action and the route test.
- Does this server change run accessibility gates? No. ADR 236 decisions 2 and 3 put them in the console repository.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
