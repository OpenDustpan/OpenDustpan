# The planner role

`planner` is a built-in role. It lets a person or a service principal see a
plan, and it does not let them change anything. Give it to a person who must
check a change before someone else applies it.

## What the role holds

The role holds each action of `viewer`, and two more:

| Action | What it allows |
| --- | --- |
| `unit:run-plan` | Start a runner job that only plans. |
| `job:read-plans` | Read the stored plans of jobs, with the values that the tool marks as sensitive masked. |

## What the role does not hold

The role does not hold these actions:

- `unit:run-apply`, `unit:run-destroy` and `unit:write-back`. A planner cannot
  apply, destroy or commit.
- `job:approve` and `job:cancel`. A planner cannot approve or cancel a job.
- `unit:read-secrets`. A planner cannot read a secret value, and the plan
  stays masked.
- `unit:write`, `unit:encrypt`, `unit:decrypt` and `unit:set-context`.

## Pulumi jobs

A Pulumi job has no stored plan. Its preview is in the step log, and
`job:read-logs` reads the step log. The role `planner` holds `job:read-plans`
and does not hold `job:read-logs`, so a planner reads a Pulumi preview only
when another grant gives `job:read-logs`.

## Secrets in a plan job

A plan job that a planner starts gets the secrets of the unit only when its
commit is on a protected branch. A plan of a planner on a branch that is not
protected gets no secrets.

## The limit of the role on a workstation

The role limits what the server runs and records. It does not limit what a
person does on their own computer. A planner who runs `tofu plan` or
`pulumi preview` on a workstation reads state with the state actions that
`viewer` gives. That plan is not a job, and the server keeps no record of it.

A person who also holds cloud credentials can change the cloud from a
workstation. The role does not prevent that. To limit it, do not give cloud
credentials to a planner.

## Give the role

Make a grant with the role `planner` on a namespace, a collection or a unit.
The grant is a stored record. You can list it, audit it and revoke it like
each other grant. `GET /api/server/roles` lists the role with its actions.
