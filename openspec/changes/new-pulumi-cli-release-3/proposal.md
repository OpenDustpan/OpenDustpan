---
version: 1
---

Issue: #2634, #2635, #3027, #3032.

## Problem

New stable releases can send reads that the newest recorded contract does not cover, so the suite must run against each release and record the result.

## Evidence

- The e2e contract directory exists on main. Proof: paths: `e2e/contract` exists true
- The version matrix policy lives in e2e/doc.go on main. Proof: paths: `e2e/doc.go` exists true; issue bodies step 4 cite the matrix policy in `e2e/doc.go`
- Four release-watch issues define the scope. Proof: issues #2634, #2635, #3027, #3032 each list one new stable release and the same four steps
- Issue #2635 names the tofu variable in parentheses. Proof: issue #2635 body step 2: (or `E2E_TOFU_VERSIONS`)

## Fix boundary

- Run the e2e suite once for each of Pulumi CLI 3.274.1, 3.275.0, 3.276.0 and OpenTofu 1.13.9
- Commit the new contract files in `e2e/contract/` with `E2E_CONTRACT_UPDATE=1 make e2e`; commit no file from a failed or partial run
- Apply the matrix policy in `e2e/doc.go`; on a contract diff the pinned version does not replace until a follow-up server fix ships

Out of scope:

- Server behavior changes for any new CLI read found by the contract diff
- Pinned-version support window changes beyond the matrix policy in `e2e/doc.go`

## Check

- Read each Pulumi changelog and run the e2e suite for 3.274.1, 3.275.0 and 3.276.0 using the driver at `e2e/tool.go:110` and the policy in `e2e/doc.go`. Each run calls ANY /api/stacks/{}/{}/{}/state/{}/resources with an access token through the stored-grant gate. Note login, state, locks, console link and discovery changes in the pull request, or note none found. Confirmed by: CI (make e2e) Check: `E2E_PULUMI_VERSIONS=3.274.1 make e2e`, `E2E_PULUMI_VERSIONS=3.275.0 make e2e`, `E2E_PULUMI_VERSIONS=3.276.0 make e2e`
- Read the OpenTofu changelog and run the e2e suite for 1.13.9 using the driver at `e2e/tool.go:110` and the policy in `e2e/doc.go`. The run calls ANY /api/v2/state-versions with an access token through the stored-grant gate. Note login, state, locks and discovery changes, or note none found. Confirmed by: CI (make e2e) Check: `E2E_TOFU_VERSIONS=1.13.9 make e2e`
- Re-run with contract update enabled and commit each NEW file in the directory `e2e/contract/`. The update run calls ANY /api/stacks/{}/{}/{}/state/{}/resources with an access token through the stored-grant gate. Commit no file from a failed or partial run. Confirmed by: CI (make e2e) Check: `git status --short e2e/contract/` shows the new files committed
- Apply the matrix policy in `e2e/doc.go`. Verification re-runs the pinned versions so each run calls ANY /api/stacks/{}/{}/{}/state/{}/resources and ANY /api/v2/state-versions with an access token through the stored-grant gate. Do not replace a pinned version while any version has an unhandled contract diff. Open a follow-up issue for a diff that needs a server fix. Confirmed by: CI (make e2e) Check: `git diff -- e2e/doc.go`
- Keep the per-version e2e run log for each of the four versions and confirm against `e2e/contract/` and `e2e/doc.go` that no skipped contract line hides a missing read before commit. The runs call ANY /api/stacks/{}/{}/{}/state/{}/resources and ANY /api/v2/state-versions with an access token through the stored-grant gate. Blocked by e2e-contract-skip-recorded-in until that fix ships. Confirmed by: CI (make e2e) Check: `E2E_PULUMI_VERSIONS=3.276.0 make e2e` plus `E2E_TOFU_VERSIONS=1.13.9 make e2e` with kept run logs

Depends on:

- e2e-contract-skip-recorded-in
- `e2e-contract-skip-recorded-in` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main can drive a real OpenTofu binary from the e2e suite. (`e2e/tool.go:110 (func OpenTofu)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change touch an accepted ADR decision? No. The pack cites no ADR; the work is a routine compatibility check.
- Does this change alter a tool-compatible API? No. The boundary is the recorded files in `e2e/contract/` and the matrix in `e2e/doc.go`. A needed server fix becomes its own change.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
