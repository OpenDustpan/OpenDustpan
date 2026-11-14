---
version: 1
---

Issue: #672, #2041.

## Problem

A developer runs a large Pulumi update, and the build at the end of the update must finish before the Pulumi CLI stops waiting.

## Evidence

- The bound in code is MaxJournalBuildDuration at 5 minutes, in pkg/stack. Proof: pkg/stack/journal_bounds.go:20
- The largest measured size in the pack is 10,000 resources. Proof: pkg/stack/journal_bounds.go:15
- The release build answers journal version 0. Proof: Issue 2041 body: The release build answers journal version 0, so the Pulumi CLI saves a full checkpoint after each step, sequentially.
- An e2e helper to cap the journal version exists. Proof: e2e/journalcap.go:29 (func NewJournalVersionCap)

## Fix boundary

- Add an e2e timing step that runs the real Pulumi CLI at 10,000 resources with journal version forced on, and records 5 build times and the CLI wait time.
- Write the result to the NEW file docs/reference/journal-build-time.md.

Out of scope:

- Retune MaxJournalBuildDuration (#672 follow-up, later issue)
- Fix journal version 0 in release builds (#2041, stays open after this change)
- Change the journaling protocol (neither issue)

## Check

- Write the e2e timing test in the NEW file e2e/journal_build_time_test.go that fails on main because no result is recorded Check: Route ANY /api/stacks/{}/{}/{}/update/{}/complete with an access token; `make e2e` fails on main
- Run the real Pulumi CLI at the tuned largest accepted size with journal version forced on through e2e/journalcap.go and record 5 build times plus the CLI wait time seen on a stalled complete call Check: Route ANY /api/stacks/{}/{}/{}/update/{}/complete with an access token; `make e2e`
- Check the 5-minute bound value in pkg/stack/journal_bounds_test.go during the run Check: No route: the test reads the bound value only; `go test ./pkg/stack -run TestJournalBuildDurationDefaultIsFiveMinutes -count=1`
- Write the margin in the NEW docs/reference/journal-build-time.md with the date, CLI version, backend, build times and wait time Check: No route: the test renders the docs file only; `make docs-lint`
- Walk the docs page from the page alone and check each error names one command Check: No route: a person reads the docs page only; usability check recorded in the pull request
- Run the full check Check: `make check` with zero findings

Depends on:

- tune-new-state-bound-real
- `tune-new-state-bound-real` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main bounds the journal build at complete with MaxJournalBuildDuration of 5 minutes. (`pkg/stack/journal_bounds.go:20`)
- Main negotiates the journal version per update. (`internal/api/pulumi/updates.go:484 (func Handler.journalVersionFor)`)
- Main can disable Pulumi journaling through a server option. (`pkg/dustpan/dustpan.go:524 (func WithDisablePulumiJournaling)`)
- The e2e suite can cap the journal version. (`e2e/journalcap.go:29 (func NewJournalVersionCap)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 43: Pulumi journaling support** (not owner-tagged).

- Decisions that apply: 6 (changed).
- Changed by: ADR 151 changes decisions 6 and 10; ADR 220 changes decision 6. Chain to the end: ADR 151, ADR 220, ADR 154, ADR 210, ADR 217, ADR 273, ADR 286, ADR 363, ADR 238, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 151, 220.
- Quote: "The corpus test passes, and the e2e run of that CLI with journaling on and off gives the same normalized export."
- Effect: The timing run reuses the journaling on and off comparison and changes no journaling behavior.
- Conflict: none.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 6 (current).
- Changed by: ADR 154 changes decisions 3, 8, 15, 28 and 29; ADR 210 changes; ADR 217 changes decision 2; ADR 220 changes decision 2. Chain to the end: ADR 154, ADR 210, ADR 217, ADR 220, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 210, 217, 220.
- Quote: "The Pulumi CLI flows stay the same. - The start of an update creates its item key. A preview also gets an item key for its events. A preview is not a history item (ADR 20, point 3), so redaction does not target it."
- Effect: The timing run keeps sealing of checkpoint blobs on.
- Conflict: none.

**ADR 220: Isolation between namespaces on shared infrastructure (owner)** (owner-tagged (all decisions)).

- Decisions that apply: 7 (current).
- Changed by: ADR 273 changes decision 12; ADR 286 changes; ADR 363 extends decisions 1, 2 and 3. Chain to the end: ADR 273, ADR 286, ADR 363.
- Current rule: this ADR, with ADR 273, 286, 363.
- Quote: "A prefix for each namespace. Each blob key of a namespace starts with `namespaces/<namespace ID>/`."
- Effect: The timing run uses one named blob backend and records it.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Write the e2e timing test in the NEW file e2e/journal_build_" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Run the real Pulumi CLI at the tuned largest accepted size w" is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: task "Walk the docs page from the page alone and check each error " is not confirmed by the agent alone: end it with one fixed phrase: "Confirmed by: CI (...)", "Confirmed by: the driver's gates." or ; a code check fails: and 1 more.

Questions considered and answered:

- Does forcing journaling on change update results? ADR 43 decision 6: the e2e run with journaling on and off gives the same normalized export.
- What if the build time reaches the CLI wait time? No new bound is set here. The run is marked failed and a later issue covers retuning.
- Does sealing change the build time? ADR 151 decision 6 and ADR 220 decision 7: the build seals blobs, so the run keeps sealing on and records the backend.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
