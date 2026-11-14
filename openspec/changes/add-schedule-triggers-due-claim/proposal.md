Issue: #1212.

## Why

**Persona: namespace administrator.** A namespace administrator sets a schedule so a unit plans itself each period. Today the tick, the due claim, the presets, and the spread do not exist, so no schedule can fire once per period across instances.

The user path this change must make work:

1. **API.** Save a schedule trigger on a unit with a preset or a 5-field UTC expression.
2. **Server start.** Wait for the minute tick; the server claims the due slot once and starts one plan job with source kind schedule.
3. **CLI.** Read the job, its plan, and its gate decision as for any scheduled job.
4. **Docs page.** Follow the docs page to set the frequency and to read the next slot.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| ClaimDue refuses a slot that the frequency does not give | Slot refused: the slot is not a time of the frequency. Check the trigger frequency and try the next slot. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |
| Save refuses a frequency below one hour or a never-run expression | Trigger save refused: the frequency is below one hour or the expression never runs. Use a preset or a 5-field UTC expression with an interval of one hour or more. | `POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| Scheduled job waits for approval or gate decision | Schedule start refused by a gate or an approval rule. The job waits as any scheduled job waits. | `GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}` |

Evidence for the claims above:

- No ClaimDue implementation exists yet, only a port comment names it. Proof: pkg/trigger/ports.go:22:// method ClaimDue for it, with conformance cases first.
- No trigger.schedule durable schedule exists in code. Proof: rg -n -F --glob '!*_test.go' 'trigger.schedule' pkg internal cmd matches 0
- Dispatcher.Scheduled already exists with a conformance harness that this change extends with the slot check. Proof: pkg/trigger/triggertest/dispatcher.go:230: first, err := h.Dispatcher.Scheduled(t.Context(), acme, "s1", slot)

## What Changes

- ClaimDue port on trigger.Store with one conditional claim per slot
- Dispatcher.Scheduled with slot check against cron Next and ErrInvalid
- Durable schedule trigger.schedule that ticks each minute with one catch-up run
- New package pkg/cron with parser, Next, presets hourly, every-6-hours, daily, and weekly, and hash spread
- Save check: minimum interval of one hour and no never-run expression
- Docs page for schedule triggers

Out of scope:

- Push triggers, follows triggers, and run order
- TFE-compatible view of triggers
- Auto-apply from a schedule; a trigger starts a plan only
- Drift schedules, which reuse pkg/cron but keep their own setting
- The 20-triggers-per-unit limit, enforced at save by run-triggers

Depends on:

- run-triggers
- `run-triggers` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The spread of first checks by hash surprises a person who expects an exact start time; the docs page shows how to read the next slot.
- UTC-only cron surprises a person in another time zone; the page shows the UTC rule with an example.
- A refused save for a short interval costs time without an example fix; the error names the one hour minimum and the save command.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "Docs in the same pull request. A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The change adds the user docs page for schedule triggers in the same pull request.
- Conflict: none.

**ADR 263: Drift detection is a plan-only refresh job with a drifted status and an opt-in schedule** (not owner-tagged).

- Decisions that apply: 14 (current), 15 (current).
- Changed by: ADR 354 extends; ADR 358 extends decision 7; ADR 366 changes decision 12. Chain to the end: ADR 354, ADR 358, ADR 366.
- Current rule: this ADR, with ADR 354, 358, 366.
- Quote: "The schedule reuses the durable schedule of issue 909. One cron parser serves both."
- Quote: "The presets are `hourly`, `every-6-hours`, `daily` and `weekly`."
- Effect: The change adds the new cron package with the same presets, UTC rule, one hour minimum, and hash spread that drift schedules use.
- Conflict: none.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 5 (current), 6 (current), 14 (changed), 15 (current), 18 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A schedule has a frequency from a list or a custom cron expression."
- Quote: "A schedule fires once per period across several instances. A schedule trigger uses the durable schedule (ADR 263 decision 14), so that of several instances one starts the job of a period."
- Quote: "One tick for all schedule triggers (question 7)."
- Effect: The change implements the schedule frequency, one start per period, the namespace service principal, the schedule source kind, and the one minute tick with one catch-up run.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only."
- Effect: The scheduled start runs as the namespace service principal under its stored grant for plan start only, with no token and no session.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "**A job token reads only what a rule names and the starter can read (15.5, option c).** A job token reads the outputs of another unit only when both checks pass at each read: - Check 1: a sharing rule of the producer names the unit of the job, its collection or its namespace. - Check 2: the principal that started the job holds `unit:read-outputs` on the producer with its own current grants, and `unit:read-secrets` for a secret output."
- Effect: The tick checks the last saver against unit:read-outputs at each read, and refuses the start when the check fails.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): Design decision 3 and issue #1212 say pkg/cron already has the parser, Next, presets and spread, but the proposal evidence shows pkg/cron does not exist. The design and the issue must say this package is new work. Resolution: answered: Design now states pkg/cron is new work; evidence no longer claims it exists.
- R1.2 (open question): The change does not name the principal that starts a scheduled plan. ADR 267 decision 14 uses the namespace service principal, and owner decision ADR 338 decision 3 limits that principal to the start of a plan, with no token or session. The change must state this grant and check it in a scenario. Resolution: answered: Spec now requires the namespace service principal and its stored grant; adr_check cites ADR 267 decision 14 and ADR 338 decision 3.
- R1.3 (failure mode): ClaimDue succeeds, then the instance crashes or the job service refuses the start. The slot is then lost with no retry, no record and no requirement. The spec needs a rule for a claim that does not lead to a job. Resolution: answered: Spec now requires a failed job start after a claim to release the claim path with a record and no silent loss.
- R1.4 (open question): The one hour minimum is not defined for custom 5-field expressions with lists or steps, such as 0,5 * * * * or */30. The save check needs a rule for how the interval is measured before it can be built. Resolution: answered: Design and spec now define the minimum as the smallest gap between consecutive Next times, including lists and steps.
- R1.5 (missing scenario): The requirements do not name the authorization actions and objects for the save, read and tick routes. Each endpoint must declare them, as the project rules require. Resolution: answered: Spec now names the authorization actions and objects for save, read, and tick.
- R1.6 (missing scenario): No scenario covers a trigger on a renamed or removed unit, or a disabled trigger. The stored reference must use the stable ID, as the project rules require. Resolution: answered: Spec now requires the stable unit ID and the disabled-trigger rule.
- R1.7 (failure mode): Each instance checks the slot against its own clock. Clock skew between instances can make one instance see a slot as due and another not. The design does not name the time source or a tolerance. Resolution: answered: Design now names the tick time as the single time source so instance clocks cannot split a slot.
- R1.8 (missing scenario): The built-in gate for a scheduled job that writes state (ADR 267 decision 3, ADR 263 decision 9) has no scenario in the spec, although the issue lists it as an acceptance point. Resolution: answered: Spec now covers the built-in schedule gate for a scheduled job with a write phase.
- R1.9 (missing scenario): The limit of 20 triggers per unit (ADR 267 decision 20) has no requirement or refusal scenario in the spec. Resolution: answered: Out of scope now states the 20-trigger limit is enforced at save by #1210.
- R1.10 (open question): The change does not say what happens to the next slot when a trigger is re-saved with a new frequency after a claim, or when the saver loses the read rights that ADR 267 decision 14 uses. Resolution: open question: Moved to open_questions with options, benchmark, and PM decision.
- R1.11 (scope): The spec fails the EARS check at line 13 (the After clause has no EARS keyword), and the Open questions section reads not spec-ready. The change cannot be implemented until both are fixed. Resolution: answered: EARS text fixed to When clauses and open_questions now holds the re-save question.
- R2.1 (wrong claim): proposal.md evidence says Dispatcher.Scheduled already has a conformance harness at pkg/trigger/triggertest/dispatcher.go:230, but design.md decision 2 and tasks 2.2 say Dispatcher.Scheduled is new work. The change must state which is true, and the cited line must exist before the claim is used. Resolution: answered: Design and evidence now state Dispatcher.Scheduled already exists with a harness and this change extends it with the slot check; tasks updated to extend, not create.
- R2.2 (failure mode): specs/trigger-schedule/spec.md 'Claim without job' says a later tick retries the next due slot. After a claim and a failed start, the failed slot is then dropped with only a record. The spec does not say whether that period must run, which conflicts with the proposal's rule of no silent loss and with ADR 267 decision 18 (one catch-up run for the latest missed slot). Resolution: answered: Claim-without-job spec now keeps the failed slot due for retry at the next tick with a record, so the period is not dropped silently.
- R2.3 (open question): The tick start does not check the read rights of the last saver. ADR 267 decision 14 and ADR 273 decision 5 (check 2) require unit:read-outputs for that principal. The provisional answer in proposal.md open question 1 does not say what happens when the last saver loses that right, so the tick cannot be built as specified. Resolution: answered: Spec now requires the tick to check last-saver unit:read-outputs and refuse with a record; open question narrowed to re-save timing only.
- R2.4 (failure mode): The provisional answer to open question 1 recomputes the next slot from the save time. A re-save just after a due slot can skip that period with no record and no run. The change needs a scenario for a save that races with a claim. Resolution: answered: Added re-save race scenario: a save between tick read and claim writes a record and the stale claim starts nothing.
- R2.5 (missing scenario): specs/trigger-schedule/spec.md 'Scheduled plan principal' covers only a successful start. It has no scenario for a revoked or missing stored grant of the namespace service principal at tick time, and the outcome and record are not stated. Resolution: answered: Added scenario for revoked or missing stored grant of the namespace service principal at tick time with refusal and record.
- R2.6 (wrong claim): proposal.md 'Questions considered' says ADR 267 decisions 5 and 18 with ADR 263 decision 15 define the minimum as one hour between consecutive fire times, and that a sub-hour gap is refused. These ADR lines state only 'a minimum interval of 1 hour'. The gap rule for lists and steps is the drafter's own choice and is not in the cited decisions. Resolution: answered: Considered answer now states the gap rule is the drafter interpretation, not ADR text.
- R2.7 (missing scenario): specs/trigger-schedule/spec.md has no scenario for a trigger whose frequency is re-saved between the tick read and the conditional claim. The design's single time source does not cover a change to the stored slot during the claim. Resolution: answered: Added scenario for frequency re-save between tick read and conditional claim.
- M.31 (duplicate): run-triggers specifies the same schedule tick, and #1210 is not a change ID. Resolution: answered: Tick stays in this change; take over run-triggers open question 0 as second open question; depends_on now names the change run-triggers and out of scope names it as owner of the trigger record.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What happens to the next slot when a trigger is re-saved with a new frequency after a claim?
   - Options: Recompute the next slot from the save time and keep the claim history as audit only; Keep the old next slot when it still matches the new frequency, else recompute from now.
   - Provisional decision (owner review 14 Nov): Recompute the next slot from the save time and keep the claim history as audit only.
   - Reason: ADR 267 does not name the re-save timing; the choice sets whether an edit causes an extra run or a missed run. The tick refusal when the last saver loses unit:read-outputs is decided by ADR 273 decision 5 and is specified, not asked here. Benchmark: Google Cloud Scheduler: a paused schedule keeps its config and starts nothing until resumed; a rewrite of the schedule recomputes the next run from now.
   - Persona impact: A namespace administrator who edits a frequency sees one clear next slot and no surprise catch-up run. Escape hatch: The person re-saves the trigger after the fix and reads the recomputed next slot from the API.
2. Which change owns the shared schedule tick taken over from run-triggers open question 0?
   - Options: This change owns the trigger.schedule tick, ClaimDue, and Dispatcher.Scheduled; run-triggers owns the trigger record and push and follows starts; Run-triggers owns the tick and this change owns only ClaimDue.
   - Provisional decision (owner review 14 Nov): This change owns the trigger.schedule tick, ClaimDue, and Dispatcher.Scheduled; run-triggers owns the trigger record and push and follows starts.
   - Reason: Both changes touch the schedule tick; one owner avoids a duplicate tick and a double start. This change keeps the tick because it holds ClaimDue and the catch-up rule. Benchmark: Google Cloud Scheduler: one scheduler owns the tick and each job owns its own trigger config.
   - Persona impact: A namespace administrator sees one tick behavior with no duplicate schedules. Escape hatch: Run-triggers keeps the trigger record routes; this change keeps only the schedule tick, ClaimDue, and Dispatcher.Scheduled.

Questions considered and answered:

- How does a cron expression map onto the durable schedule? ADR 267 decisions 5, 6, and 18 answer it: use the presets and the 5-field UTC cron syntax, fire once per period through the durable schedule, and tick once per minute with one catch-up run for the latest missed slot.
- Does a scheduled start skip gates and approvals? ADR 267 decisions 2 and 9 answer it: the trigger starts a plan only through the job service, and gates, approvals, freezes, locks, and limits apply as to any job.
- What source kind does the job record? ADR 267 decision 15 answers it: a schedule trigger records the source kind schedule on the job.
- Which principal starts a scheduled plan? ADR 267 decision 14 with ADR 338 decision 3 answers it: the start runs as the namespace service principal under its stored grant for plan start only.
- How is the one hour minimum measured for lists and steps in a custom expression? ADR 267 decision 5 and ADR 263 decision 15 state only a minimum interval of 1 hour. The drafter interprets this as the smallest gap between two consecutive Next times, so a list or step with a sub-hour gap is refused as the drafter choice, not as ADR text.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
