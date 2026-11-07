# M5-8b: start apply and destroy jobs: the stored plan, the unit lock of the apply phase, the Pulumi destroy check

- Class: CORE (job model, apply path; security review of the destroy check)
- Routing: Sonnet author, Opus review
- Branch: `m5-8b-apply-destroy-lift`
- Conflict group: `job-start`
- Persona path: P5 runner apply, steps `apply_job_starts`,
  `stored_plan_applies` (#2610); milestone 5 exit test steps 2 and 5.
- Depends on: #2619 (M5-2, plan upload, commit and read through the launcher
  session) and #2611 (M5-8a, step lists, the refusal, `ErrNoPlan`). Both are
  queued, not on main. Do not start until both merge. Start from the main
  that has both.

## Facts checked at main 9e96ae30c8c3

- `pkg/dustpan/jobservice.go:529-530`: the refusal "this server starts plan
  jobs only, not %s" (`job.ErrUnsupported`). `stepsOf` at line 542.
  `applySteps` and `destroySteps` are NOT on main; #2611 adds them.
- `pkg/job/job.go:64`: `ErrUnitBusy` "another job holds the unit for a change".
- `pkg/job/artifact.go:147`: `CheckPlan`. #2611 adds `ErrNoPlan`.
- `pkg/inventory/inventory.go`: `Entry.Lock *Lock` (`Since`, `Holder`,
  `Operation`), the unit lock flag; `ManualLock` is apart.
- GLOSSARY "Unit lock" and "Manual lock". "hold" is under _Avoid_ for a
  manual lock: in copy, say "unit lock".
- #2611 "Left for the change that lifts the refusal" lists: the delivery of
  the stored plan to the apply phase; the Pulumi destroy binding (JSON
  destroy preview at apply time, refuse a URN that the approved preview lacks,
  a unit lock on the stack for the whole apply phase, a stack version check at
  update start); the route declaration of the start route (`unit:run-plan`
  only); a catalog code for `ReasonNoPlan`.
- Re-check each fact above after #2619 and #2611 merge, and list the main
  sha in the PR.

## Files it may touch

- `pkg/dustpan/jobservice.go` (lift the refusal for apply and destroy; the
  destroy step list)
- `pkg/job/artifact.go` (`CheckPlan` for an operation with no plan file),
  `pkg/job/` new file for the destroy preview binding, and tests
- `pkg/job/jobtest/` new cases
- `internal/api/jobs/routes.go` (the action list of the start route: `unit:run-apply`, `unit:run-destroy`)
- `internal/errcode` (a code for `ReasonNoPlan`), `openapi.json`, `clients/typescript` (by the generators)
- The inventory projection write of the unit lock for a job, in the store
  adapters, only if the unit lock of a job is not already projected (check first)
- `docs/reference/jobs-api.md`

## Files it must not touch

- `internal/api/runnerapi/*` and `pkg/job/planparts.go` (M5-2 owns them; use their routes)
- `pkg/job/service.go` `Approve` (M5-13a)
- `pkg/launcher/*`, the runner repository

## Change

1. Lift the refusal for `apply` and `destroy` on runners. Keep the refusal
   for edits, deliveries and a plain refresh as #2611 has it.
2. Apply phase of OpenTofu and Pulumi `up`: the launcher reads the stored plan
   through `GET /runner/v1/session/plan/parts/{part}` (M5-2). The apply phase
   has no other source of a plan.
3. Pulumi destroy: the plan phase runs `destroy --preview-only --json` and
   stores the set of URNs as the plan of the phase. The apply phase runs the
   JSON preview again before `destroy --yes --skip-preview`, and refuses with
   a fixed reason when a URN is not in the approved set. Compare exact
   strings. A preview that does not parse fails closed.
4. Unit lock: the apply phase takes the unit for the whole phase, and the
   inventory shows it as the unit lock with the holder (the job ID) and the
   operation (`apply` or `destroy`). A Pulumi update start of another holder
   during the phase gets the tool's lock error. At update start, check that
   the stack version is the version of the plan; else fail the phase stale.
5. Route declaration and the error code of `ReasonNoPlan`.

## Acceptance

- `pkg/job` and `pkg/dustpan` tests: golden step lists for apply and destroy
  of each tool still pass `job.ValidateSteps`.
- Hostile, each alone: a destroy preview at apply time with one extra URN;
  with a URN that differs only in case; a preview that is not JSON; a preview
  over the size limit; a stack version that moved after the plan; a second
  job that tries to take the unit during the apply phase (`ErrUnitBusy`).
- Conformance: the unit lock shows during the apply phase and is gone after
  the end, on SQLite and PostgreSQL.
- e2e: `TestJobsAPI` gets an apply step for a stack and a workspace on a
  runner, through the stored plan. If #2610 is on main, un-skip
  `apply_job_starts` and `stored_plan_applies`.
- `docs/reference/jobs-api.md` says that apply and destroy are accepted, and
  documents the destroy check and the unit lock.
- `make check` and `make e2e` zero findings.
