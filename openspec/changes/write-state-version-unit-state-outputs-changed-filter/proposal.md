---
version: 2
---

Issue: #1215.

## Why

**Persona: developer.** Developer runs a stack that follows another stack. Each apply of the source starts a plan even when no shared output changed. The noise hides real changes. The server must start follower plans only when the root outputs changed.

The user path this change must make work:

1. **API.** Save a follows trigger with when outputs-changed on unit B that names unit A as source.
2. **CLI.** Apply unit A with the Pulumi CLI or OpenTofu, first with no output change, then with a changed root output.
3. **Docs page.** Read why the first apply started nothing and the second started a plan.
4. **E2e.** Run the e2e step with real CLI binaries over TLS on SQLite and PostgreSQL.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save of a follows trigger with an unknown when value fails. | 400 with code trigger.invalid and a message that names the allowed values applied and outputs-changed. | `curl -X PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| Apply of source with unchanged outputs starts no follower plan. | No new plan job for followers with when outputs-changed; the trigger read still shows when outputs-changed. | `curl /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |

Evidence for the claims above:

- Dispatcher.Upstream is the entry point that decides follows starts. Proof: pkg/trigger/ports.go:136:// Outcome is the result of one trigger in a Dispatcher.Upstream call.
- A test helper drives Dispatcher.Upstream with an applied final version. Proof: pkg/trigger/triggertest/dispatcher.go:76:	out, err := h.Dispatcher.Upstream(t.Context(), trigger.Applied{Unit: a, Version: applied})
- Issue #1215 asks for the outputs-changed value, the digest at final write, and the compare in Dispatcher.Upstream. Proof: Part of #909. Outcome: A `follows` trigger can start a plan only when the root outputs of the source changed since its last apply.

## What Changes

- Root-outputs digest written at each apply-counting final state version, with no secret value in the digest or in any answer
- Field value outputs-changed for the when field of a follows trigger on the trigger routes
- Compare in Dispatcher.Upstream that skips starts when the digest matches the immediate prior apply-counting final digest with version-pair binding
- Docs page and e2e step with real CLI binaries that applies twice, once with a changed output

Out of scope:

- Final state versions, current pointers and write paths
- Follows trigger cycle check, caps and start dispatch
- Schedule and review-request-push triggers
- Output sharing rules and reads

Depends on:

- write-state-version-unit-state-follows-trigger-start
- write-state-version-unit-state
- `write-state-version-unit-state-trigger-caps-depth` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A skipped start surprises: the docs page names the dispatcher outcome reason outputs-unchanged.
- Two words applied and outputs-changed cost a read; the docs page shows one example of each.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Dispatcher.Upstream decides follows starts. (`pkg/trigger/ports.go:192`)
- Test helper drives Dispatcher.Upstream with an applied final version. (`pkg/trigger/triggertest/dispatcher.go:76`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 13 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A `follows` trigger has a field `when` (question 2)."
- Quote: "The value `outputs-changed` comes in a later slice."
- Effect: Implements the later slice: the value outputs-changed of the field when and the compare before start.
- Conflict: none.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (not owner-tagged).

- Decisions that apply: 1 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "**A job reads the outputs of another unit on two paths, each with two checks.**"
- Effect: Keeps the digest inside the server; the digest grants no read.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request."
- Effect: Ships the docs page for the when value in the same pull request.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: task "Add failing dispatcher test: unchanged outputs skip, changed outputs s" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Write the digest at final write and compare in Dispatcher.Upstream aga" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: task "Run the full check: wiring, route actions, e2e, docs, usability." runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part>"; a code check fails: and 1 more.

Questions considered and answered:

- Should outputs-changed ship now or stay a later slice? ADR 267 decision 13 answers it: default applied, outputs-changed comes in a later slice; this change is that slice.
- What does the server compare? Issue #1215 answers it: a digest of the root outputs stored at final write; it never leaves the server and holds no value.
- Does an outputs-changed start bypass policy checks? ADR 267 decision 2: gates, approvals, freezes, locks and open-job limits apply as to any job.
- Which version is prior, and does a secret-only change count? The immediate previous apply-counting final version of the same source (ADR 267 decision 12). Secret plain values become a fixed marker; names, types and secret marks stay in the input, so a secret-only value change stays equal and a secret to plain flip differs.
- Is a migration backfill needed? No backfill and no numbered migration. The baseline holds the new column. A version without a digest counts as changed once at the next compare.
- What does a retry or replay compare? The version pair of the triggering apply. A replay gives the same pair and the dispatcher starts at most 1 plan per follower per source version.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
