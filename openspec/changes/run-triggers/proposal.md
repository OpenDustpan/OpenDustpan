Roadmap: `docs/roadmap.md:319` (Run triggers).

## Why

**Persona: developer.** Developer runs plans from a push, a schedule and a follows link. The developer creates a trigger on a stack or workspace, the server starts a plan job on each event, and the developer reads the plan and the record of the start.

The user path this change must make work:

1. **Docs page.** Open the run triggers guide and list the triggers of the stack or workspace.
2. **API.** Create a review-request-push, schedule or follows trigger on the unit.
3. **E2e.** Fire the trigger and read the started plan job.
4. **API.** Read the plan job and the audit entry of the trigger start through the API.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| create trigger without grant | 403 unit:manage needed on the stack or workspace | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| save above the per-unit or per-source limit | 422 trigger limit: unit holds 20 triggers | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers` |
| trigger fires while the unit is locked, frozen or awaiting approval | 409 trigger blocked: unit locked or frozen, job waits or is refused | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}` |

Evidence for the claims above:

- This change extends the trigger guide at docs/guides/run-triggers.md Proof: docs/guides/run-triggers.md
- This change extends the trigger domain code at pkg/trigger with store at pkg/store/internal/triggerstore and API at internal/api/triggers Proof: pkg/trigger
- This change extends the trigger e2e suite at e2e/triggers_test.go Proof: e2e/triggers_test.go

## What Changes

- CRUD routes for unit triggers with declared auth actions and audit entries
- Plan-only trigger start through the job service
- Guide page for run triggers

Out of scope:

- Push intake, webhook signature and replay checks
- Schedule tick, due claim, catch-up and deduplicate
- Follows chain, cycle refusal text, depth guard and outputs-changed filter
- Output sharing rules and secret output reads
- Pull request automation first slice comment and status
- Apply after the merge and pre-merge apply option
- Write-back commits
- CLI-driven remote execution
- Run order across units

Depends on:

- add-job-service-routes-start
- job-service-unit-settings-pool
- runners
- start-plan-on-push-review
- add-schedule-triggers-due-claim
- write-state-version-unit-state-follows-trigger-start
- write-state-version-unit-state-trigger-caps-depth
- write-state-version-unit-state-outputs-changed-filter
- `job-service-unit-settings-pool` (order of the milestone review)
- `runners` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A follower that never fires surprises the user; the trigger read shows the last fire time, the last error and the source unit in tool words.
- A cycle error must name each unit in order with the names each tool uses, so the user edits the right trigger.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 9 (current).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "A trigger belongs to one unit and has one kind. The unit is the unit that the trigger starts. The kinds are `review-request-push`, `schedule` and `follows` (24.1). A unit is a stack or a workspace of any tool, and the tr"
- Quote: "A trigger starts a job through the job service. It has no path of its own. The gates, the approval rules, the change freezes, the manual locks and the limits of open jobs apply to the job as to any job."
- Quote: "A trigger starts a plan and nothing else. The acceptance criteria name a plan for each kind. The port that a trigger uses to start a job can start a plan and nothing else. No trigger starts an apply in these slices (RFC"
- Effect: The change stores one trigger of one kind per unit and starts only a plan through the job service. Push intake, schedule tick and follows chain stay in their own changes.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "**A job token reads only what a rule names and the starter can read (15.5, option c).** A job token reads the outputs of another unit only when both checks pass at each read: - Check 1: a sharing rule of the producer names the unit of the job"
- Effect: A job that a trigger starts reads outputs only when a sharing rule names it and its last saver holds unit:read-outputs on the producer.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only. The principal still has no token and no session. The rule of ADR 267 decision 14 about the read check of the last saver does not change."
- Effect: Trigger starts run as the namespace service principal with the stored plan-only grant, with no token and no session.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The ADR check cites a quote from ADR 310 that is not in the ADR file, so the spec-ready gate fails; the proposal must copy the decision text exactly before implementation. Resolution: answered: Removed the ADR 310 entry; run order stays out of scope in out_of_scope with no quote.
- R1.2 (wrong claim): The proposal claims ADR 267 decision 14 is current, but the input marks it as changed by ADR 273 and ADR 338; the ADR check lists it as current and the Effect line does not show the changed rule. Resolution: answered: ADR 267 decision 14 now marked changed with ADR 273, ADR 299 and ADR 338 chain in effect; added ADR 273 and ADR 338 entries.
- R1.3 (failure mode): No requirement covers double fire after an outage or across instances for push and follows starts; the risk list names duplicate plans but the spec only covers the schedule slot, so a restart during a source apply can start the follower twice. Resolution: answered: Added single-fire idempotency requirement with deduplicate scenarios for push, schedule and follows.
- R1.4 (missing scenario): The spec has no scenario for a refused or failed trigger start: a locked unit, a freeze, an approval hold or a missing stored plan-start grant on the service principal. The user path in the proposal has no error for these. Resolution: answered: Added blocked-start requirement with locked, frozen and approval-hold scenarios and a trigger-blocked error.
- R1.5 (missing scenario): The spec does not state the run-time depth guard for follows chains, although ADR 267 decision 17 requires it and the design risk names it. No scenario tests a chain that is valid at save but loops at run time. Resolution: answered: Added run-time depth-guard requirement with chain-refused scenario.
- R1.6 (missing scenario): The push trigger has no requirement for matching the review request target repository, branch and paths (ADR 267 decision 19), so a push on a wrong branch or path has no test. Resolution: answered: Added push-match requirement with repository, branch and paths scenarios.
- R1.7 (failure mode): Concurrent saves of triggers in one namespace are required to serialize (ADR 267 decision 17), but the spec and tasks do not name a test for two simultaneous saves that could skip the cycle check. Resolution: answered: Added serialized-saves scenario under the cycle requirement and a concurrent-save task check.
- R1.8 (open question): The design says the start uses the namespace service principal with a stored plan-start grant (ADR 338 decision 3), but the plan-start grant creation and its owner are not described in the change; the owner must confirm the grant is seeded at namespace creation. Resolution: open question: Added owner-decided open question on seeding time of the stored plan-start grant.
- R1.9 (scope): The proposal lists the console job timeline and masked plan diff as part of the user path, but no task or requirement covers the console; the definition of done omits it. Resolution: answered: Console removed from the promised path; console step now reads only data this change exposes, with no console build task.
- R1.10 (wrong claim): The proposal cites the 'run triggers' guide and the e2e suite as existing evidence, but the change also creates them in tasks 2.4 and 2.3; the evidence claim is not consistent with the task list. Resolution: answered: Evidence claim now states this change extends the existing guide, code and e2e suite.
- R2.1 (adr conflict): tasks.md and proposal.md ADR check: ADR 267 decision 14 is quoted as current in the ADR check of proposal.md while the decision state is changed; the change relies on decision 14 for the start identity (namespace service principal with plan-only grant) but never states the changed text it implements, so the build cannot be checked against the rule. Resolution: answered: Removed changed decision 14 from the ADR 267 decisions list and quotes; the effect now states the build follows the current rule in ADR 338 with ADR 273.
- R2.2 (open question): ADR 267 decision 17 and the design require a depth guard at run time, but the spec does not give the depth limit value or the action on refusal beyond recording the chain; the build cannot be done without the number. Resolution: open question: Added a PM-decided open question for the run-time depth guard number; the spec uses the configured guard until decided.
- R2.3 (failure mode): Single-fire requirement covers restarts but not an outage of the durable schedule store or a partial failure between plan start and the stored last-fire record; no scenario says whether the trigger retries, skips or starts twice. Resolution: open question: Added a PM-decided open question and deduplicate scenarios for partial failure between plan start and last-fire record.
- R2.4 (missing scenario): The blocked-start requirement says the server refuses the start or holds the job, but does not say which one applies per gate, so the trigger read and the error text in proposal.md cannot be built as written. Resolution: answered: Split blocked-start scenarios into refuse for lock and freeze and wait for approval hold, with the same outcome as a manual start.
- R2.5 (missing scenario): No scenario for a missing or revoked stored plan-start grant on the namespace service principal, although ADR 338 makes the grant the only start permission. Resolution: answered: Added a missing-grant scenario that refuses the start and writes a denial audit entry.
- R2.6 (missing scenario): No scenario for a trigger whose source unit is deleted or renamed; the follows edge refers to a unit reference, so the stable ID rule of project.md applies and no requirement states it. Resolution: answered: Added a source-removed requirement that stores the source by stable ID and skips the start with a recorded reason.
- R2.7 (failure mode): Push match requirement does not cover hostile webhook input: a forged or replayed forge event could start plans; no signature or replay scenario is listed in the layers table or tasks. Resolution: answered: Added forge signature and replay guards with hostile tests in the handler layer and tasks.
- R2.8 (wrong claim): The proposal says the ADR 267 quote for decision 17 concerns serialized writes, but the design layer test covers only the service path; the concurrent-save task 2.3 proves saves in the trigger package and not through the HTTP route. Resolution: answered: Changed the concurrent-save task to prove serialization through the HTTP route with parallel saves.
- R2.9 (scope): Task 2.4 requires real runners and real forge events for the e2e step, which is outside the plan-only start this change owns and is not named in the definition of done. Resolution: answered: Narrowed the e2e task to the real CLI and the real directory with the started plan visible, and named runners in depends_on.
- R2.10 (open question): Provisional decision seeds the plan-start grant at namespace creation, but existing namespaces created before this change have no seeded grant; the migration path for them is not stated and no baseline migration rule is applied. Resolution: open question: Changed the seeding recommendation to ensure at creation and lazily at first save, which covers existing namespaces with no migration.
- M.11 (duplicate): The push start, the schedule tick, the follows start, the cycle refusal and the depth guard duplicate start-plan-on-push-review, add-schedule-triggers-due-claim and write-state-version-unit-state. Resolution: answered: Narrowed this change to trigger CRUD, the plan-only start rule, the step-14 e2e step and the guide. Removed push, schedule single-fire, follows chain, cycle, depth guard, blocked-start and limits requirements; they stay in start-plan-on-push-review, add-schedule-triggers-due-claim and the write-state-version-unit-state changes named in depends_on and out_of_scope. Removed the tick-retry and depth-number open questions from this change.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. When is the stored plan-start grant of the namespace service principal seeded for new and existing namespaces?
   - Options: Ensure the plan-start stored grant at namespace creation and lazily at first trigger save; Seed the grant at first trigger save only.
   - Provisional decision (owner review 14 Nov): Ensure the plan-start stored grant at namespace creation and lazily at first trigger save
   - Reason: ADR 338 states the stored plan-only grant but not the seeding time, and existing namespaces need cover with no baseline migration. Benchmark: AWS IAM roles for service-linked starts seed the role at resource creation with least privilege
   - Persona impact: A Developer sees no extra step; the trigger start works on new and old namespaces with no manual grant. Escape hatch: An operator lists the stored grant of the namespace service principal through the access review and revokes the trigger when the grant is absent.
   - Owner-tagged ADRs involved: ADR 338 (their words do not change).

Questions considered and answered:

- Does a trigger start an apply? ADR 267 decisions 2, 3, 9 and 16: every trigger starts a plan through the job service with the same gates, approvals, freezes, locks and limits as any job, and never starts an apply.
- How are follows cycles handled? ADR 267 decisions 7, 10, 17 and 21: refused at save with the ordered unit path, serialized writes, cross-namespace follow refused by the fence. Owned by the follows chain changes.
- How do scheduling, routing and identity work? ADR 267 decisions 4, 6, 15, 18 and ADR 338 decision 3: pool comes from unit settings; one durable tick starts one job per period; start runs as namespace service principal with plan-only stored grant; source kinds schedule, forge, trigger. Tick and push intake owned by their own changes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
