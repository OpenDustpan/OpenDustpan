# 304. Native run-all: fan-out applies in milestone 9

Status: accepted · 2026-11-01. Decided by the project owner on 1 November 2026

Changes:

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
6. **The shim is not a new job kind.** This answers the open question of RFC
   109: `run-all` is one job for each Terragrunt unit, under a change order.
   The job kinds of ADR 100 stay. The state-operation questions of RFC 109
   stay open: import, `state rm` and `state mv` as jobs, and the question of
   whether a state operation needs a gate.
7. **Profiles and blueprints stay the recommended path (owner).** OpenDustpan
   supports Terragrunt for teams that already use it. The documentation does not
   present it as the first choice for a new team.
8. **Open points go to an RFC.** The RFC for milestone 9 decides these
   points. This ADR does not decide them:
   - Unit discovery: how the shim finds the Terragrunt units and maps each
     one to a unit of OpenDustpan.
   - Dependency order: how the order between Terragrunt units reaches the
     apply jobs. The apply jobs of one change order have no order today (ADR
     190, consequences and open questions).
   - A failure in one unit: what happens to the other units when one apply
     job fails. A change order moves to `partially-applied` today (ADR 190
     decision 7).

## Consequences

- RFC 109 says that its `run-all` question has an answer, and links this
  ADR. Its state-operation questions stay open.
- The roadmap gets milestone 9 after milestone 8, with the work items and the
  exit test below.
- ADR 282 gets a "Changed by" item for this ADR.
- No code lands before the project owner accepts the RFC of decision 8.
- A runner shim needs a change order for each `run-all`. A team that does not
  want a review in every case sets its approval rules (ADR 225), as for any
  change order.
- A fan-out over more than 100 units, or over units of more than one
  namespace, hits the limits of a change order (ADR 190 decisions 3 and 12).
  The RFC of decision 8 says what the shim does then.

## Exit test of milestone 9

On Linux, on PostgreSQL, through the real binary over TLS, with real
OpenTofu and real Terragrunt, the e2e suite does these steps. Each step
passes or fails on the fact that it names.

1. **Local run-all.** `terragrunt run-all apply` on a laptop over 3 units
   uses OpenDustpan as the backend. Pass: the suite finds 3 workspaces, each
   with 1 new state version and 1 history entry, and no change order.
2. **Run-all in the CI of a team.** The same command in a CI job gives the
   same result as step 1.
3. **Run-all on a runner.** `terragrunt run-all apply` over 3 units runs on
   an OpenDustpan runner. Pass: the suite finds 1 change order with 3 plan
   jobs and 3 apply jobs, each job on its own unit. Each unit has 1 new state
   version and 1 history entry. No job runs more than one unit.
4. **The review holds.** The change order of step 3 starts no apply job
   before the approvals of its requirements are in place. Pass: the apply
   jobs start after the apply request of a person, and the provider log has
   no apply call before that time.
5. **A failure in one unit.** One of the 3 units fails at the apply. Pass:
   the failed unit has its failed apply job and no new state version, the
   other two units have their result, and the change order shows the state
   that the RFC of decision 8 names.
6. **A job does not apply two units.** Pass: the provider log shows no unit
   applied by the job of another unit.
