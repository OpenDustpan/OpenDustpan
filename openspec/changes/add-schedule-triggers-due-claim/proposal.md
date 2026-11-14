---
version: 2
---

Issue: #1212.

## Why

**Persona: namespace administrator.** A namespace administrator sets a schedule so a stack or workspace plans itself each period. Today the tick, the due claim, the presets and the spread do not exist, so no schedule can fire once per period across server processes.

The user path this change must make work:

1. **API.** Save a schedule trigger on a unit with a preset or a 5-field UTC expression.
2. **Server start.** Wait for the minute tick; the server claims the due slot once and starts one plan job with source kind schedule.
3. **CLI.** Read the job, its plan and its gate decision as for any scheduled job.
4. **Docs page.** Follow the docs page to set the frequency and to read the next slot.
5. **E2e.** Run the e2e step that saves a daily trigger and reads one scheduled plan job.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| ClaimDue refuses a slot that the frequency does not give | Slot refused: the slot is not a time of the frequency. Read the trigger and use a slot that it gives. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| Save refuses a frequency below one hour or a never-run expression | Trigger save refused: the frequency is below one hour or never runs. Use a preset or a 5-field UTC expression with an interval of one hour or more. | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| Scheduled job waits for approval or gate decision | The job waits as any scheduled job waits. Read the job to see the gate. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |

Evidence for the claims above:

- No ClaimDue implementation exists; only a port comment names it. Proof: pkg/trigger/ports.go:22:// method ClaimDue for it, with conformance cases first.
- No trigger.schedule durable schedule exists in code. Proof: rg -n -F --glob '!*_test.go' 'trigger.schedule' pkg internal cmd matches 0
- Dispatcher.Scheduled already exists with a conformance harness that this change extends. Proof: pkg/trigger/triggertest/dispatcher.go:230: first, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", slot)
- The package pkg/cron does not exist yet. Proof: The context pack path check for pkg/cron gives exists: false.

## What Changes

- ClaimDue port on trigger.Store with one conditional claim per slot and a 2 minute lease
- Dispatcher.Scheduled with slot check against cron Next and ErrInvalid, with pause after 5 failures
- Durable schedule trigger.schedule that ticks each minute with one catch-up run
- New package pkg/cron with parser, Next, presets and hash spread
- Save check in internal/api/triggers: minimum interval of one hour and no never-run expression
- Docs page for schedule triggers

Out of scope:

- Push triggers, follows triggers and run order
- TFE-compatible view of triggers
- Auto-apply from a schedule; a trigger starts a plan only
- Drift schedules, which keep their own setting
- The 20-triggers-per-unit limit, enforced at save by run-triggers

Depends on:

- run-triggers
- `run-triggers` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The hash spread surprises a person who expects an exact start time; the docs page shows how to read the next slot.
- UTC-only cron surprises a person in another time zone; the page gives an example.
- A refused save names the one hour minimum and the save command.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Dispatcher.Scheduled exists as an interface method. (`pkg/trigger/ports.go:180`)
- trigger.Store is the store port. (`pkg/trigger/ports.go:23`)
- The trigger HTTP adapter holds the store. (`internal/api/triggers/triggers.go:59`)
- The durable scheduler starts one operation per tick. (`pkg/durable/scheduler_test.go:208`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change adds the docs page for schedule triggers in the same pull request.
- Conflict: none.

**ADR 263: Drift detection is a plan-only refresh job with a drifted status and an opt-in schedule** (not owner-tagged).

- Decisions that apply: 14 (current), 15 (current).
- Changed by: ADR 354 extends; ADR 358 extends decision 7; ADR 366 changes decision 12. Chain to the end: ADR 354, ADR 358, ADR 366.
- Current rule: this ADR, with ADR 354, 358, 366.
- Quote: "The schedule reuses the durable schedule of issue 909. One cron parser serves both."
- Quote: "The presets are `hourly`, `every-6-hours`, `daily` and `weekly`."
- Effect: The new cron package gives the same presets, UTC rule, minimum and spread that drift schedules use.
- Conflict: none.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 5 (current), 6 (current), 15 (current), 18 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A schedule has a frequency from a list or a custom cron expression."
- Quote: "A schedule fires once per period across several instances. A schedule trigger uses the durable schedule (ADR 263 decision 14), so that of several instances one starts the job of a period."
- Effect: The change implements the frequency, one start per period, the schedule source kind and the one-minute tick with one catch-up run.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only."
- Effect: The scheduled start runs as the namespace service principal under its stored grant, with no token and no session.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: evidence path `pkg/cron` does not exist in the checkout; a code check fails: evidence path `pkg/cron` does not exist in the checkout; a code check fails: task "Add a case to the Dispatcher.Scheduled suite in pkg/trigger/triggertes" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: and 3 more.

Questions considered and answered:

- How does a cron expression map onto the durable schedule? ADR 267 decision 18: one durable schedule ticks each minute, the store keeps the next slot, and one catch-up run starts for the latest missed slot.
- Does a scheduled start skip gates and approvals? No. ADR 267 decisions 2 and 3: the start goes through the job service and all gates apply.
- Which principal starts the plan? ADR 267 decision 14 with ADR 338 decision 3: the namespace service principal.
- How is the one hour minimum measured? As the smallest gap between two consecutive Next times; ADR 267 decision 5 sets the minimum interval of 1 hour.
- What happens to the next slot at a re-save? The change recomputes it from the save time; a stale claim is refused with a record.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
