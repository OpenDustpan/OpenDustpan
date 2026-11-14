Issue: #1316, #1346, #1383, #1384.

## Why

**Persona: developer.** A developer tries to run a plan on a runner without naming the pool or code location on each call. The server must take those inputs from the settings of the stack or workspace so trigger starts and API starts work the same way, with refusals that name the one command that helps.

The user path this change must make work:

1. **Docs page.** Namespace administrator saves code location, pool, profile reference and steps on the unit from the docs page.
2. **API.** Developer starts a plan job through POST /jobs with no location; server uses the stored settings.
3. **API.** Schedule or push trigger starts a plan on a runner with the stored settings; job reads outputs of its source under the sharing rule and saver check.
4. **E2e.** Operator runs the e2e step against the real CLI to prove the refusal and the trigger read denial.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Start with a pool or location override without the needed grant. | 403 with the missing action and no state change; run the reach check to list the grants of the caller. | `opendustpan status` |
| Trigger or API start with no location in request and none in unit settings. | 422 with the missing field and no job created; run the health check then set the unit settings from the docs page. | `opendustpan doctor` |
| Job-token read of outputs of another unit with no sharing rule or with a saver who lost access. | 403 on the output read with no secret value in the body; run the check command then fix the sharing rule or the grant of the saver. | `opendustpan check --help` |

Evidence for the claims above:

- The trigger manager records that location, pool and steps come from the settings of the unit. Proof: pkg/trigger/manager.go:271: // Location is the code location of the plan, or the zero value. The job + pkg/trigger/manager.go:272: // service checks unit:define-steps on the unit for a location, for the + pkg/trigger/jobstarter/jobstarter.go:46: // location, the runner pool and the steps come from the settings of the unit,
- The saver resolver over principal records exists and the read check type exists without a handler call. Proof: pkg/dustpan/triggers.go:250: // saverRecords is the trigger.SaverResolver over the principal records. + pkg/trigger/readcheck.go:15: // OutputReadCheck.Allow at each read.
- No handler calls the trigger output-read check and no stored trigger field exists on the job record. Proof: rg -n -F --glob '!*_test.go' 'trigger.OutputReadCheck.Allow' pkg internal cmd matches 0; rg -n -F --glob '!*_test.go' 'job.Job.Trigger' pkg internal cmd matches 0

## What Changes

- Unit settings hold code location, runner pool, profile reference and default steps with namespace defaults as fallback.
- Job start resolves inputs from request override then unit settings then defaults, snapshots them on the job, and checks unit:define-steps and namespace:manage-runners for overrides.
- Job-token output reads enforce the sharing rule plus the trigger saver and manual-fire caller check with unit:read-outputs at each read.
- Built-in roles keep unit:run-plan in the planner role, grant unit:define-steps to the lead role, and keep namespace:manage-runners in the administrator role, with a refusal e2e step.

Out of scope:

- Pulumi default step design of the runner recorder beyond a refused start with the docs command
- Profile provider port, open sessions and secret resolve beyond storing the profile reference in settings
- Write-back, stored-plan binding, pull-request automation and console run page

Depends on:

- add-job-service-routes-start

## Usability

Where the path still costs the person time or surprises them:

- A developer who names a pool or location without the grant gets a 403 that names the action and the one reach command.
- A trigger start with no stored location is refused at fire time with the settings command, not with an empty run that stays queued.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 267: Run triggers: three kinds, a plan only, started through the job service** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current), 9 (current), 14 (changed).
- Changed by: ADR 273 changes decision 14; ADR 299 changes decisions 14 and 19; ADR 307 extends decision 8; ADR 310 changes decision 22; ADR 338 changes decision 14. Chain to the end: ADR 273, ADR 299, ADR 307, ADR 310, ADR 338, ADR 286, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 273, 299, 307, 310, 338.
- Quote: "The runner pool is the only routing key (6.1). A trigger has no pool and no label."
- Quote: "A trigger starts a job through the job service. It has no path of its own."
- Quote: "A trigger starts a plan and nothing else."
- Quote: "The principal of a start that no person makes (question 3). - The start runs as the namespace service principal."
- Effect: Trigger starts use the job service with the same checks as other jobs, take the pool from the unit settings, start a plan only, and run as the namespace service principal with the saver read check.
- Conflict: none.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (not owner-tagged).

- Decisions that apply: 1 (changed), 2 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 303, ADR 336, ADR 328, ADR 359.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "**A job reads the outputs of another unit on two paths, each with two checks.**"
- Quote: "**A job that only plans decrypts the secrets of its own unit only from trusted code.**"
- Effect: Job-token reads of outputs of another unit keep both checks at each read. A plan-only job gets own-unit secrets only from trusted code.
- Conflict: none.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 1 (current), 5 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "**A job token reads only what a rule names and the starter can read (15.5, option c).**"
- Effect: The saver part of the read uses unit:read-outputs of the last saver and the manual-fire caller, with a sharing rule that names the unit, its collection or its namespace.
- Conflict: none.

**ADR 338: The namespace service principal holds a stored grant** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The principal still holds nothing else (owner, 2026-11-07). The grant covers the start of a plan only."
- Effect: The namespace service principal keeps only the stored grant to start a plan and the saver read rule does not change.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (adr conflict): The spec and tasks make the last saver lose unit:read on the source. ADR 273 decision 14 (changed by ADR 273) and ADR 213 decision 1 check 2 require unit:read-outputs for the saver. The check uses the wrong action. Quote ADR 273 decision 14 and fix the spec scenario and task 1.2. Resolution: answered: Spec scenario, task and design now use unit:read-outputs for the saver; adr_check cites ADR 273 decisions 1 and 5 with the exact quote from the pack.
- R1.2 (wrong claim): The 403 and 422 error rows name commands that do not exist in the CLI: opendustpan access and opendustpan units. The code check fails on them, so the proposal cannot pass the spec-ready gate. Replace them with commands that exist. Resolution: answered: Error commands now use opendustpan status, opendustpan doctor and opendustpan check --help.
- R1.3 (missing scenario): No scenario covers a start with no location in the request and none in the unit settings (the 422 path), nor a Pulumi start with no default steps, which the design says is refused with the docs command. The 'Trigger start with stored location' scenario is the only path shown. Resolution: answered: Added scenarios for no location in request and none in settings with 422, and for Pulumi with no stored default steps refused.
- R1.4 (open question): The spec-ready gate is not passed: two open questions are decided by PM, and the role mapping for unit:run-plan, unit:define-steps, job:read and namespace:manage-runners is not fixed. The tasks in 2.2 cannot be built until the role answer is recorded. Resolution: open question: Role mapping stays an open question for the owner; build of the role catalog waits for that answer.
- R1.5 (wrong claim): The spec says a trigger start with a stored location runs with default steps. Issue 1316 says Pulumi has no default steps, and the design leaves them undefined until the recorder step design lands. The scenario promises steps the design does not provide. Resolution: answered: Spec and design state that a Pulumi start with no stored steps is refused with the docs command; no scenario promises Pulumi default steps.
- R1.6 (adr conflict): The ADR check lists ADR 267 decision 14 as a decision that applies and as changed, but quotes no line of it. The 'Conflict: none' claim for the saver check has no quoted evidence. Resolution: answered: ADR 267 decision 14 is now quoted exactly from the pack text.
- R1.7 (failure mode): Settings change between the queueing of a job and its planning, or between two reads of one job. The change does not say which settings snapshot the planner uses, or whether a read uses the grant state of its start. Resolution: answered: Design records the start snapshot rule: resolved inputs freeze at start, reads use the grant state at each read.
- R1.8 (failure mode): A trigger whose stored location or unit is deleted or changed before it fires is not covered. The design refuses a start with no stored location, but the spec has no scenario for a fire after the settings are removed. Resolution: answered: Added scenario for a fire after the stored location is removed, refused with 422.
- R1.9 (missing scenario): The usability text says a trigger start is refused at save time. No requirement or scenario checks a save-time refusal of a trigger with no stored location, so the spec and the usability claim disagree. Resolution: answered: Usability and spec now agree: refusal happens at fire or start time, not at trigger save time; no save-time refusal is claimed.
- R1.10 (scope): The change adds profile resolution and a profile opener to the planner, but the proposal puts the profile provider port out of scope beyond naming the profile in settings. The boundary between the two items is not stated. Resolution: answered: Design and out_of_scope state the boundary: this change stores only the profile reference; provider open and secret resolve stay in the profile item.
- R2.1 (open question): The role mapping (unit:run-plan to starter, unit:define-steps to lead, namespace:manage-runners to administrator) is chosen by the author and no ADR or owner decision answers it. Issue 1346 asks the owner to decide the intended behavior, yet Open questions reads none. Name the decider or record an owner answer. Resolution: open question: Role mapping moved to open_questions for the owner to decide; spec and tasks mark the mapping as the recommended option only.
- R2.2 (failure mode): The profile reference is stored as a name in unit settings and kept in the job snapshot. project.md requires a stored reference to a renamable entity to use the stable ID. No decision covers a renamed or removed profile after the snapshot. Resolution: answered: Design stores the profile stable ID and spec adds rename and removal scenarios.
- R2.3 (missing scenario): No scenario says a trigger or API start under a manual lock is refused. ADR 267 decision 2 says manual locks apply to a trigger-started job, and ADR 213 decision 10 (owner-tagged) puts manual locks on units and collections. The start path does not check them. Resolution: answered: Spec adds manual-lock refusal scenario for trigger and API starts.
- R2.4 (missing scenario): The saver read check requires a sharing rule that names the job unit. ADR 273 decision 5 also allows a rule that names the collection or the namespace of the job. No scenario covers those cases. Resolution: answered: Spec adds collection and namespace sharing-rule scenarios.
- R2.5 (scope): Issue 1384 asks to classify job:read-plans and job:read-logs with job:read. The tasks and the role requirement classify only job:read, so part of the item is missing. Resolution: answered: Role requirement and tasks now classify job:read-plans and job:read-logs with job:read.
- R2.6 (wrong claim): The ADR check lists the ADR 338 chain for ADR 267 decision 14 but does not quote or check ADR 338 decision 3, which is owner-tagged and restates the principal rule the trigger start depends on. The Conflict none claim lacks that check. Resolution: answered: adr_check adds ADR 338 decision 3 with its exact quote.
- R2.7 (missing scenario): The design table says a pool override without namespace:manage-runners gets 403. The spec has no scenario for a principal that holds unit:define-steps but not namespace:manage-runners, so the pool-only refusal is not tested. Resolution: answered: Spec adds pool-only override refusal scenario.
- R2.8 (failure mode): No scenario covers a snapshot pool that is deleted or has no runners between queueing and running. The change does not say whether the job waits, fails or is refused. Resolution: answered: Design and spec state a job with a deleted pool stays queued.
- R2.9 (open question): ADR 213 decision 2 says a plan-only job decrypts secrets of its own unit only from trusted code. The change resolves a profile reference from a stored snapshot for starts that no person makes, but does not state how that rule applies to those starts. Resolution: answered: considered and design state the ADR 213 decision 2 trusted-code rule for trigger plans.
- R2.10 (wrong claim): The evidence cites a code comment at pkg/trigger/manager.go:271 as proof that settings hold no code location. A comment does not prove the state of the code. Give the rg output that shows no settings field. Resolution: answered: Evidence no longer claims absence from a comment; it cites the resolver intent and the zero-match rg outputs.
- M.34 (consistency): The recommendation gives unit:run-plan to a 'starter' role. ADR 212 and step 3 need the planner role to hold unit:run-plan. Resolution: answered: Role mapping now keeps unit:run-plan in the planner role; spec, open question and tasks state the planner role and this change depends on add-job-service-routes-start.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which built-in roles hold unit:run-plan, unit:define-steps, job:read, job:read-plans, job:read-logs and namespace:manage-runners?
   - Options: planner role keeps unit:run-plan, lead role gets unit:define-steps, administrator keeps namespace:manage-runners; maintainer role gets unit:run-plan and unit:define-steps, administrator keeps namespace:manage-runners.
   - Provisional decision (owner review 14 Nov): planner role keeps unit:run-plan, lead role gets unit:define-steps, administrator keeps namespace:manage-runners
   - Reason: AWS IAM gives a read, a configure and a manage action to distinct roles; the planner starts with stored settings, the lead changes location and steps, and only the administrator routes pools. Benchmark: AWS IAM
   - Persona impact: A developer with the planner role starts a plan with stored settings; an override still needs the extra grant. No persona loses a working path. Escape hatch: An administrator holds every action, so a start still works before the owner answers.

Questions considered and answered:

- Where does the pool and code location of a trigger start come from? ADR 267 decision 4 answers it: the job takes the pool of the unit settings and the trigger holds none. The request location is an override that needs unit:define-steps; the stored location is the default.
- What does a job-token read of outputs of another unit check? ADR 213 decision 1 answers the two checks and that a grant change applies at the next read. ADR 267 decision 14 with ADR 273 answers the saver part: check 2 uses unit:read-outputs of the last saver. The namespace service principal holds only the plan start per ADR 338 decision 3.
- How does the plan-only secret rule apply to starts that no person makes? ADR 213 decision 2 answers it: a plan-only job gets own-unit secrets only from trusted code. The stored profile reference does not change that rule; a trigger plan with no trusted code gets no secrets of its own unit.
- Do manual locks stop a trigger start? ADR 267 decision 2 answers it: gates, approval rules, freezes, manual locks and limits of open jobs apply to a trigger job as to any job. ADR 213 decision 10 is owner-tagged and puts manual locks on units, collections and namespaces.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
