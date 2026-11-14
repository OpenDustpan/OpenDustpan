Roadmap: `docs/roadmap.md:648` (A strict case takes turns).

## Why

**Persona: developer.** A developer changes one workspace while another team applies to it. The change reads a data source, so the server cannot prove it disjoint. The developer needs the apply to take turns on the whole-unit lock and leave a record of why it waited. A runner job that finds the lock held ends as blocked, and the job service starts it again when the lock is released.

The user path this change must make work:

1. **API.** Start two runner jobs on one workspace through the jobs API. Job A plans a disjoint change. Job D plans a change that reads a data source.
2. **CLI.** Watch job D with `dustpan job show <job-id>` while job A applies. Job D shows the status blocked, the holder and the case data-source, then starts again when the lock of A is released.
3. **Docs page.** Read the history of the workspace and see the strict case data-source on the item of D.
4. **E2e.** Run the e2e step with real OpenTofu and a fake cloud provider log that shows ordered apply periods. BLOCKED by RFC 129 point 4 (the job on the TFE-compatible path).

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A direct state write arrives while the whole-unit lock is held for a strict apply | The server answers 423 with the lock holder and the strict case. Run `dustpan job show <job-id>` to see the holder. | `dustpan job show <job-id>` |

Evidence for the claims above:

- No related change and no path of this feature exist in the pack. Proof: related_changes: [] and paths: []
- The e2e suite drives real CLI binaries. Proof: e2e/e2e_test.go
- The roadmap step names the data source case, the whole-unit lock and the history entry. Proof: docs/roadmap.md:648

## What Changes

- A saved plan that reads a data source makes the claim strict with the case data-source.
- A runner job whose plan needs the whole-unit lock and finds it held turns the refusal into the status blocked. The job service starts it again when the lock is released.
- The local wrapper waits for a strict case with the retry and the timeout of ADR 368 decision 5.
- The history item of the strict apply names the case.

Out of scope:

- The other five strict cases
- The merge of an upload one resource instance at a time
- The local CLI wrapper code (local-run-two-cases); this change states only its wait rule
- Splitting a plan with -target
- Claim ID, parent pointer, fence and serial rules, which ADR 368 decides and other changes build

Depends on:

- ADR 368 on main (pull request #3052)
- two-disjoint-claims
- concurrent-applies-merge-core
- `two-disjoint-claims` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- Job show names the holder and the case.
- The history uses plain words for the case.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The roadmap step names the strict data source case. (`docs/roadmap.md:648`)
- The workspace lock has the error code `workspace.locked` for a lock that another principal holds. (`internal/errcode/catalog_workspace.go:15 (WorkspaceLocked)`)
- The workspace service takes the whole-unit lock. (`pkg/workspace/service.go:731 (Service.Lock)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 368: Concurrent applies: apply claims, the server serial, fences and the closure test** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "This ADR adds no case to the six cases of ADR 302 decision 2 and takes none away."
- Quote: "The history shows the apply claim and the job of each merged state version."
- Effect: The data source case keeps the whole-unit lock. The history entry uses the apply claim ID of ADR 368.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: ADR 368 extends decision 6. Chain to the end: ADR 368.
- Current rule: this ADR, with ADR 368.
- Quote: "Work that the server cannot prove disjoint takes turns."
- Quote: "An RFC for concurrent applies comes first."
- Effect: The change covers the data source case only and gives it the whole-unit lock. It adds no claim field. ADR 368 decision 7 keeps the six cases.
- Conflict: none.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 6 (extended).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220.
- Quote: "The server mints a lock ID for each acquisition."
- Effect: The strict apply uses the existing lock of the workspace.
- Conflict: none.

## Open questions

1. How does the server find the apply claim of a request on the TFE-compatible path: one principal for each job, or another credential that names the job? Decides: owner.
   - Options: The job-ID claim of the job token names the apply claim, with the person as the principal; A new service principal for each job; A header that names the apply claim.
   - Recommended: none. RFC 129 point 4 stays open: the owner did not take its recommendation on 14 November 2026 and gives the industry view later (ADR 368, "Not decided here"). This change needs it for the e2e step, which writes state through the TFE-compatible routes with a job token.

Not spec-ready: RFC 129 point 4 is open for the owner.

Questions considered and answered:

- Which strict case does this change cover? Data sources only. ADR 302 decision 2 lists six cases. The other five are out of scope.
- Does a runner job with a data source plan wait on the whole-unit lock, or end as blocked? It ends as blocked and the job service starts it again when the lock is released; that is how it takes turns. ADR 302 decision 2 (owner): "A runner job turns the refusal into the status "blocked". The local `dustpan` wrapper waits: it tries the claim again after the refusal until the blocking claim ends, or until a timeout." The owner's approved M8 answer of 14 November 2026 (decisions week log): "a data-source plan waits on the whole-unit lock". Decided by the PM under the decision rule on 14 November 2026. The local wrapper waits with ADR 368 decision 5.
- Which record fields does the history entry use for the case and the claim? ADR 368 decision 3: the parent, the base and the apply claim ID of each state version.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
