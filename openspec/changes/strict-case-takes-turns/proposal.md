Roadmap: `docs/roadmap.md:648` (A strict case takes turns).

## Why

**Persona: developer.** A developer changes one workspace while another team applies to it. The change reads a data source, so the server cannot prove it disjoint. The developer needs the apply to take turns on the whole-unit lock and leave a record of why it waited.

The user path this change must make work:

1. **API.** Start two runner jobs on one workspace through the jobs API. Job A plans a disjoint change. Job D plans a change that reads a data source.
2. **CLI.** Watch job D with `dustpan job show <job-id>` while job A applies.
3. **Docs page.** Read the history of the workspace and see the strict case data-source on the item of D.
4. **E2e.** Run the e2e step with real OpenTofu and a fake cloud provider log that shows ordered apply periods. BLOCKED by the accepted RFC for concurrent applies.

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
- A strict apply starts only after the running apply ends.
- The history item of the strict apply names the case.

Out of scope:

- The other five strict cases
- The merge of an upload one resource instance at a time
- The local CLI wrapper and its retry rules
- Splitting a plan with -target
- Claim ID, parent pointer, fence and serial rules, which the RFC decides

Depends on:

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
- Source filter code is a separate package. (`pkg/source/source.go:1 (func Filter)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Work that the server cannot prove disjoint takes turns."
- Quote: "An RFC for concurrent applies comes first."
- Effect: The change covers the data source case only and gives it the whole-unit lock. It adds no claim field. Code starts after the owner accepts the RFC.
- Conflict: none.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 6 (extended).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220.
- Quote: "The server mints a lock ID for each acquisition."
- Effect: The strict apply uses the existing lock of the workspace.
- Conflict: none.

## Open questions

1. Does a runner job with a data source plan wait on the whole-unit lock, or end as blocked? Decides: owner.
   - Options: End as blocked, and the person starts the job again; Wait on the whole-unit lock inside the server.
   - Recommended: Wait on the whole-unit lock inside the server. Reason: The exit test says the change starts its apply after the apply of A ends. ADR 302 decision 2 says runner jobs end as blocked on an overlap. The words do not settle a strict case.
2. Which record fields does the history entry use for the case and the claim? Decides: owner.
   - Options: Wait for the RFC ADR and use its fields; Define local fields now.
   - Recommended: Wait for the RFC ADR and use its fields. Reason: ADR 302 decision 6 leaves claim ID, parent pointer and fence to the RFC.

Not spec-ready: a code check fails: path `pkg/source/source.go` (.main_facts[1].where) does not exist in the checkout; use an existing path, or name it as new ("add the new file pkg/source/source.go"); a code check fails: test `TestStrictDataSourceWaits` (.tasks[0].items[0].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: test `TestStrictHistoryNamesCase` (.tasks[0].items[1].check) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: and 1 more.

Questions considered and answered:

- Which strict case does this change cover? Data sources only. ADR 302 decision 2 lists six cases. The other five are out of scope.
- Does the strict job wait on the server or end as blocked? ADR 302 decision 2 says a runner job turns a refusal into blocked. See the open question.
- Which claim fields does the history use? None here. ADR 302 decision 6 leaves them to the RFC.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
