Roadmap: `docs/roadmap.md:638` (Record history, audit, and sealed values for merges.).

## Why

**Persona: developer.** A developer runs OpenTofu against one shared workspace with another team and then reads history. Without the claim on each state version and one merged audit entry, the developer cannot tell which apply made which change or prove that no resource was lost.

The user path this change must make work:

1. **API.** Run two disjoint applies through runner jobs and check the state version list for two new state versions with claim of each state version.
2. **API.** Read the merged audit entry for the workspace and confirm each claim.
3. **Docs page.** Read the merge history guide for the meaning of parent pointer and claim ID.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Claim of a merged state version is missing in history. | History for the workspace shows no claim. Call `ANY /api/v2/workspaces/{}/state-versions` and check the claim field of each version. | `ANY /api/v2/workspaces/{}/state-versions` |
| Merged audit entry is missing or incomplete. | Audit entry for the merge is missing a parent. Call `ANY /api/audit/entries` with the unit filter and check the merged entry. | `ANY /api/audit/entries` |
| Merge refused for unsealed secret bytes. | Merge output holds a secret in plain text. Fix the state bytes so each secret stays sealed, then retry. Call `ANY /api/v2/workspaces/{}/state-versions` to confirm no version was written. | `ANY /api/v2/workspaces/{}/state-versions` |

Evidence for the claims above:

- Exit test requires history with two new state versions and the claim of each state version. Proof: part: Pass: the provider log shows that the two apply periods overlap in time, the final state has the change of A and the change of B, and the history shows two new state versions, and the history shows the claim of each state version.
- This part records parent pointer and claim ID and shows the claim in history, and it does not change base serving or merge commit logic. Proof: This change is part 2 of 2 of a split of `concurrent-applies`. Its scope: This part records parent pointer and claim ID on each new version and shows the claim in history. It adds the merged audit entry and keeps sealed values correct. It does not change base serving or merge commit logic. That logic stays in the first part.
- Workspace state version list route exists. Proof: ANY /api/v2/workspaces/{}/state-versions
- Audit entries read route exists. Proof: ANY /api/audit/entries

## What Changes

- Store parent version ID and claim ID on each merged state version in the baseline
- Show the claim of each state version in the existing state version list
- Write one merged audit entry per merge with each claim in the same transaction
- Keep sealed values sealed across merges with no plain secrets in audit

Out of scope:

- Base state version serving per claim
- Merge commit logic one resource instance at a time
- Claim registration and overlap refusal
- Local CLI wrapper behavior
- Sealed detection rule for merge output

Depends on:

- concurrent-applies-merge-core
- `concurrent-applies-merge-core` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- History with two new versions and two claim IDs can surprise a person who expects one linear entry per apply.
- Audit with one merged entry can confuse a person who searches per claim without a claim filter.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Audit domain defines the audit-read kind. (`pkg/audit/audit.go:69`)
- History conformance suite exists and covers history reads (func TestHistoryConformance). (`pkg/inventory/inventorytest/mem_test.go:164`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 1 (current), 3 (changed).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220.
- Quote: "Storage. The metadata of workspaces, state versions and locks lives in SQL."
- Quote: "It needs a higher serial than the current state."
- Effect: Version metadata stays in SQL and content stays in blobs. This part only stores and shows parent and claim identifiers that merge-core commits. It changes no lineage or serial rule.
- Conflict: none.

**ADR 298: PostgreSQL failover readiness** (not owner-tagged).

- Decisions that apply: 2 (current), 8 (current).
- Changed by: ADR 337 changes decision 9. Chain to the end: ADR 337.
- Current rule: this ADR, with ADR 337.
- Quote: "Requests fail fast with 503. During the gap, each request that needs the database gets 503 with `Retry-After: 5`"
- Quote: "A database transaction commits all its rows or none. A connection that breaks before `COMMIT` rolls the transaction back on the server."
- Effect: Merge record commits follow fail-fast and all-or-none transaction rules. A 503 or a broken connection leaves no partial version and no partial audit entry.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The server merges each upload one resource instance at a time."
- Quote: "This ADR does not decide these points. The RFC answers them."
- Effect: This part records only what merge-core commits and does not decide serial assignment, fence per claim, or disjointness. Those points stay with the RFC and with concurrent-applies-merge-core.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this change alter base serving or merge commit logic? No. Base serving and one-resource-at-a-time commit stay in concurrent-applies-merge-core. This part only stores parent version ID and claim ID, shows them in history, writes one merged audit entry, and keeps sealed bytes opaque. Cited in ADR 302 decisions 2 and 6.
- Does this change decide serial assignment, fence per claim, or disjointness? No. Serial assignment, fence per claim, claim registration, overlap refusal, and closure test stay with the RFC and merge-core. This part depends on merge-core and records its result. Cited in ADR 302 decision 6.
- Are version, claim link, and audit entry atomic? Version rows and the merged audit entry commit in one fenced transaction per ADR 298 decisions 2 and 8. A restart, a fence loss, a 503, or an audit write failure leaves no new version and no merged entry. Cited in ADR 26 decision 1 and ADR 298 decision 8.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
