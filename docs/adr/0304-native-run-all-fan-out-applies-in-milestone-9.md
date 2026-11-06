# 304. Native run-all: fan-out applies in milestone 9

Status: accepted · 2026-11-01. Decided by the project owner on 1 November 2026

Changed by:

- ADR 310 changes decision 8: `state rm` and `state mv` as jobs.

Changes:

- Changes ADR 100 decision 2: a discovery job is the only job kind with no unit.
- Changes ADR 100 decision 20: the job token of a discovery job covers the namespace of the job and holds one stored grant.
- Changes ADR 282 decision 5: answers the open question "A job kind for `terragrunt run-all` that makes many runs?".

## Context

[RFC 109](../rfc/0109-job-kinds-for-state-operations-and-many-runs.md) holds
the gaps of RFC 6 that no ADR decided. One gap is `terragrunt run-all`. This
command changes many workspaces in one command. A job produces one Pulumi
update or one OpenTofu run (ADR 100), so the command does not fit one job.
ADR 282 decision 5 left the question open and moved it to RFC 109. RFC 109
asks: "Is `run-all` one job for each Terragrunt unit, or a job kind that
produces many runs?"

The project owner decided on 1 November 2026 (owner): "teams can already
run-all via our other mechanisms. We can add native run-all support/fan-out
applies to M9. Locally and in CI it should already just work. On dustpan
runners we can just shim it to fan out via our native feature instead of
running all the terraform applies in a single app."

The facts that the decision uses:

- Terragrunt is a wrapper that a team runs in place of `tofu`. A `generate`
  block in the Terragrunt configuration writes a `cloud` block for the
  TFE-compatible API. So each Terragrunt unit talks to its own workspace.
  This works on a laptop, in the own CI of a team, and as a job wrapper on a
  runner (ADR 282).
- A change order (ADR 190) is the native object for a change of many units
  that one review covers. A change order belongs to one namespace. Each plan
  revision holds 1 to 100 units. A plan job runs for each unit of each
  revision, and an apply job runs for each unit of the approved revision (ADR
  190 decisions 3 and 5, ADR 191). The change order holds the approvals, the
  signatures and the threads (ADR 190 decision 6, ADR 192, ADR 193). A person
  asks for the apply, and the durable operation `changeorder.apply` starts
  one apply job for each unit (ADR 190 decision 10, ADR 197). Version 1 runs
  runner jobs only (ADR 190 decision 5).

## Decision

1. **OpenDustpan supports Terragrunt as a wrapper (owner).** A team that already
   uses Terragrunt keeps its configuration. Terragrunt works with OpenDustpan
   as the TFE-compatible API backend of OpenTofu: on a laptop, in the own CI
   of a team, and as a job wrapper on a runner. This ADR adds no new rule
   for these three places.
2. **A team can already change many units in one reviewed change (owner).**
   The native mechanism is the change order of ADR 190, with its plan
   revisions, reviews and threads (ADRs 191 to 198). A team that does not use
   Terragrunt, or that wants one review for many units now, uses a change
   order.
3. **Milestone 9 adds native run-all support: fan-out applies (owner).**
   Milestone 9 comes after milestone 8 in the roadmap.
4. **Locally and in the CI of a team, `terragrunt run-all` keeps working
   unchanged (owner).** OpenDustpan adds no wrapper, no flag and no change
   to the Terragrunt configuration. Each Terragrunt unit talks to its own
   workspace, with its own lock, its own state versions and its own history.
5. **On OpenDustpan runners, a shim turns `terragrunt run-all` into the
   native fan-out (owner).**
   - The shim makes one job for each Terragrunt unit. The jobs run under the
     native multi-unit object: a change order (ADR 190).
   - The runner does not run all the applies in one process. Each unit
     applies in its own apply job, with its own state version and its own
     history entry.
   - The change order keeps its rules: the approvals, the signatures and the
     threads are on the change order, and a person asks for the apply (ADR
     190 decisions 6 and 10).
6. **The discovery job (PM, for the owner).** The shim creates nothing by
   itself. The model is a setup job that emits a dynamic matrix of child
   jobs, which then run in the same run and for the same actor.
   - The wrapper job that runs `terragrunt run-all` is a discovery job. It
     finds the Terragrunt units and the order of their dependencies. It
     writes no state.
   - The discovery job is a job kind of the tool-neutral job model (ADR
     100). It belongs to a namespace. It has no unit, no run of its own and
     no state write. It is not a plan job and not an apply job of a unit.
   - It is the only job kind with no unit. Every other job keeps "one job,
     one unit" (ADR 100 decision 2). So this ADR changes that decision for
     this one kind.
   - The job token of a discovery job (ADR 100 decision 20) covers the
     namespace of the job, not a unit. It holds one stored grant:
     `changeorder:author` on that namespace. The grant has a condition on the
     job-ID claim, so it works only for a token of that job. A user can list
     it as a grant. It ends when the job ends.
   - With that grant, the token can create the change order of this job and
     attach its revision. It can do nothing else.
   - The author of the change order is the principal that requested the
     wrapper job. The runner is never the author, so ADR 191 decision 2
     stays as it is.
   - Each plan job and each apply job records the change order and the
     discovery job that caused it, with the reason "terragrunt run-all
     fan-out". The history and the audit entry of each job show them.
7. **The shim lives in the runner and the launcher.** It uses only the
   change order API. The TFE-compatible API adapter has no run-all logic.
8. **The fan-out jobs are not a new job kind.** This answers the open
   question of RFC 109: `run-all` is one job for each Terragrunt unit, under
   a change order. The plan jobs and apply jobs are the job kinds of ADR 100
   and ADR 190. The discovery job of decision 6 is the one new kind. The
   state-operation questions of RFC 109 stay open: import, `state rm` and
   `state mv` as jobs, and the question of whether a state operation needs a
   gate.
9. **Profiles and blueprints stay the recommended path (owner, same
   conversation).** The owner said, right before the decision quoted in the
   context: "I would recommend teams use profiles and blueprints".
   OpenDustpan supports Terragrunt for teams that already use it. The
   documentation does not present it as the first choice for a new team.
10. **Open points go to an RFC.** [RFC 115](../rfc/0115-terragrunt-run-all-fan-out-on-runners.md)
    holds them. This ADR does not decide them:
    - Unit discovery: how the discovery job finds the Terragrunt units and
      maps each one to a unit of OpenDustpan.
    - Dependency order: ordered apply jobs, or a change order that does not
      order its apply jobs (ADR 190, consequences and open questions).
    - A failure in one unit: what happens to the other units when one apply
      job fails.

## Consequences

- RFC 109 says that its `run-all` question has an answer, and links this
  ADR. Its state-operation questions stay open.
- The roadmap gets milestone 9 after milestone 8, with the work items and the
  exit test below.
- ADR 282 gets a "Changed by" item for this ADR.
- No code lands before the project owner accepts RFC 115.
- A runner shim needs a change order for each `run-all`. A team that does not
  want a review in every case sets its approval rules (ADR 225), as for any
  change order.
- A fan-out over more than 100 units, or over units of more than one
  namespace, hits the limits of a change order (ADR 190 decisions 3 and 12).
  RFC 115 says what the shim does then.

## Exit test of milestone 9

On Linux, on PostgreSQL, through the real binary over TLS, with real
OpenTofu, real Terragrunt and a test provider that records each call with a
time, the e2e suite does these steps. Each step passes or fails on the fact
that it names.

1. **Local run-all.** `terragrunt run-all apply` on a laptop over 3 units
   uses OpenDustpan as the backend. Pass: the suite finds 3 workspaces, each
   with 1 new state version and 1 history entry, and no change order.
2. **Run-all in the CI of a team.** The same command in a CI job gives the
   same result as step 1.
3. **Run-all on a runner.** `terragrunt run-all apply` over 3 units runs on
   an OpenDustpan runner. Pass: the suite finds 1 discovery job, 1 change
   order, 3 plan jobs and 3 apply jobs. Each plan job and each apply job is
   on its own unit. Each unit has 1 new state version and 1 history entry.
   The call record of the test provider shows no unit applied by the job of
   another unit.
4. **The record names the cause.** Pass: the history entry and the audit
   entry of each plan job and each apply job name the discovery job, the
   principal that requested the wrapper job, and the reason "terragrunt
   run-all fan-out". The change order names the same principal as its
   author. The list of grants shows the grant of the discovery job while the
   job runs. The grant ends with the job.
5. **The review holds.** Pass: the apply jobs start only after the apply
   request of a person, and the call record of the test provider has no
   apply call before that time.
6. **A failure in one unit.** One of the 3 units fails at the apply. Pass:
   the failed unit has a failed apply job and no new state version, and the
   other 2 units show the result that RFC 115 decides.
