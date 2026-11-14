Roadmap: `docs/roadmap.md:722` (The record names the cause).

## Why

**Persona: developer.** A developer runs terragrunt run-all on runners and then reads history and audit entries to confirm what caused each plan and each apply across many stacks and workspaces.

The user path this change must make work:

1. **CLI.** Run terragrunt run-all apply on a runner; the shim fans out into one plan job and one apply job per unit under a change order.
2. **API.** Open the history entry of one unit and read the discovery job link, the requesting principal, and the fan-out reason.
3. **API.** Open the matching audit entry and confirm the same discovery job, requesting principal, and reason.
4. **Docs page.** Follow the Terragrunt fan-out guide to repeat both reads.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A reader opens a non-fan-out history entry and sees no cause link. | No cause is present because the entry is not from fan-out; confirm the job ID through ANY /api/audit/entries and check that the entry has no cause fields. | `GET /api/audit/entries` |
| A reader opens the audit entry of a fan-out plan job and the requesting principal was later removed. | The stored entry is unchanged; re-read it through `GET /api/audit/entries/{id}` and confirm the same discovery job ID and principal ID with the subject copy from write time. | `GET /api/audit/entries/{id}` |

Evidence for the claims above:

- Main has history and audit packages with list and read routes. Proof: Pack main lists pkg/history, pkg/audit, ANY /api/audit/entries, and ANY /api/audit/entries/{}.
- No e2e file in the pack covers an apply job. Proof: Pack evidence for keyword Apply job lists e2e_count 0 with empty e2e, guides, and code lists.
- Plan-job coverage exists in named e2e files. Proof: Pack evidence for keyword Plan job lists e2e/claim_gate_test.go, e2e/jobs_api_test.go, and e2e/launcher_test.go among 7 e2e files.

## What Changes

- Store discovery job ID, requesting principal stable ID with subject copy, and fixed reason terragrunt run-all fan-out on every fan-out plan-job and apply-job history entry
- Store the same three cause values on every fan-out plan-job and apply-job audit entry
- Reference discovery job and principal by stable ID so renames and removals keep the link

Out of scope:

- Discovery of Terragrunt units and dependency order
- Approval rules and when apply jobs start
- Behavior when one unit fails
- Local run-all and run-all in team CI

Depends on:

- run-all-on-runner-discovery
- run-all-on-runner-fanout-plan-apply-jobs

## Usability

Where the path still costs the person time or surprises them:

- A reader must open two places, history and audit, to confirm one cause; the docs page shows both reads in order.
- The fixed reason string is machine readable but a new reader must learn its exact value before filtering.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs history conformance tests. (`pkg/inventory/inventorytest/mem_test.go:164 (func TestHistoryConformance)`)
- Main tests audit chain families. (`pkg/access/audit_chain_families_test.go:11 (func TestAuditChainFamilies)`)
- Main checks the reason for missing grant actions. (`pkg/access/reach_reason_test.go:33 (func TestMissingActionsReason)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 304: Native run-all: fan-out applies in milestone 9** (owner-tagged (all decisions)).

- Decisions that apply: 5 (current), 6 (current), 7 (current), 8 (changed).
- Changed by: ADR 310 changes decision 8. Chain to the end: ADR 310.
- Current rule: this ADR, with ADR 310.
- Quote: "Each unit applies in its own apply job, with its own state version and its own history entry."
- Quote: "The wrapper job that runs `terragrunt run-all` is a discovery job."
- Quote: "The shim lives in the runner and the launcher. It uses only the change order API."
- Quote: "The plan jobs and apply jobs are the job kinds of ADR 100 and ADR 190."
- Effect: The change keeps per-unit jobs and entries, links each fan-out entry to the discovery job and requesting principal, and puts no run-all logic in the TFE-compatible adapter.
- Conflict: none.

**ADR 310: Run order, run options, per-run values, sources without git and state operations as jobs** (owner-tagged (all decisions)).

- Decisions that apply: 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Each tool adapter maps it to a command of its tool."
- Effect: The change does not alter state-operation job kinds; it only records provenance on plan jobs and apply jobs.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Should fan-out jobs share one history entry or keep per-unit entries with a cause link? ADR 304 decisions 5 and 6 answer it: each unit keeps its own history entry, and the wrapper job is a discovery job that writes no state; provenance therefore links child entries to the discovery job.
- Does the TFE-compatible API need a new run-all field? ADR 304 decision 7 answers it: the shim uses only the change order API, so no new TFE wire field is added.
- Does the discovery job itself carry a cause? ADR 304 decision 6 answers it: the discovery job has no unit and writes no state, so it has no history entry and its own audit entry carries no cause.
- Does main already record the fan-out cause? The pack proves the gap: Apply job has e2e_count 0 with empty lists, so a failing e2e step for fan-out cause is new work.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
