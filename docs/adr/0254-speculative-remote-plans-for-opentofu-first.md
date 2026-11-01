# 254. Remote execution for OpenTofu starts with speculative plans on runners

Status: accepted · 2026-10-20. Decided by the product manager under the owner's delegation of 20 October 2026. Accepts part of RFC 30. Supersedes in part ADR 25 decisions 1 and 3 (no remote plans, `operations: false` and `execution-mode: local` for each workspace). Uses ADRs 100, 102, 135 and 199

Changed by:

- ADR 300 changes decision 6: the log read of OpenTofu uses a signed URL for each job, phase and principal.

## Context

Decided by the product manager under the owner's delegation of 20 October
2026, with the owner's decision rule.

[RFC 30](../rfc/0030-opentofu-remote-execution.md) asks for `tofu plan` on a
workstation to run remotely and stream its log back. RFC 1 and ADR 25 serve
OpenTofu with `operations: false`, so each plan runs on the workstation.
ADRs 100 to 103 and 130 to 138 now give a job model, runners in the network
of the team, a launcher and a source pipeline. ADR 199 gives a snapshot of
files with no git commit. So the parts that RFC 6 named as missing exist.

We read the OpenTofu source (`opentofu/opentofu`, `main` at `42f15c9`,
2026-10-19, with checks against `v1.11.9`) and go-tfe `v1.95.0`, the
version that OpenTofu `v1.11.9` pins. The e2e matrix uses OpenTofu
1.11.14, 1.12.7 and 1.13.1:

- The `cloud` block reads `GET /api/v2/organizations/:org/entitlement-set`.
  It runs locally when `operations` is `false`, or when the workspace has
  `execution-mode` `local` (an exact string compare,
  `internal/cloud/backend.go`).
- A speculative plan sends these requests, in this order
  (`internal/cloud/backend_plan.go`, `backend_common.go`):
  1. `POST /workspaces/:id/configuration-versions` with `speculative=true`
     and `auto-queue-runs=false`.
  2. A `PUT` of a tar.gz to the `upload-url`, with no token.
  3. Polls of the configuration version until `uploaded`, for about 30
     seconds.
  4. `POST /runs`.
  5. `GET /runs/:id/run-events` and `GET /runs/:id?include=task_stages`.
  6. Polls of the run.
  7. `GET /plans/:id`, and reads of its `log-read-url` with `limit` and
     `offset`.
- While a run is `pending`, the CLI also reads the workspace and
  `GET /organizations/:org/runs/queue`. It reads the runs of the
  workspace unless this run is the current run. It reads
  `GET /organizations/:org/capacity` only when the queue position is
  greater than 0. An error of any of these stops the
  plan. An error of `run-events` stops the plan too. The CLI reads cost
  estimates, policy checks and task stages only when the run names them.
- `tofu apply` and `tofu plan -out` send `speculative=false`.
  `tofu plan -out` also sends `provisional=true`, which makes a saved
  cloud plan.
- The CLI sends values of `-var`, `-var-file` and environment variables as
  run variables, with no sensitive flag (`internal/cloud/cloud_variables.go`).
- After a remote apply, the CLI reads no state version. Its outputs come
  from the log.

A speculative plan changes no resource and writes no state. So it is the
smallest slice that a team can use, and it needs no answer on approvals of
an apply through the TFE API.

## Decision

1. **The next run slice for OpenTofu.** Remote execution through the
   TFE-compatible API is the next run protocol slice for OpenTofu. Slice 1
   serves speculative plans only. It serves no apply.
2. **Opt in for each workspace.** The entitlement `operations` is `true`
   for a namespace only when the namespace has a runner pool. Each
   workspace keeps `execution-mode` `local` until a person with the action
   to manage the workspace sets it to `agent` through the API (ADR 17).
   So the behavior of each workspace today does not change, and a team
   with no runner sees no change.
3. **A configuration version is a file set.** The upload of a
   configuration version becomes a file set of the unit (ADR 199). The
   `upload-url` is a URL on the server, for one upload, signed, with a
   short expiry and the limits of the pool (ADR 199 decision 8). It never
   redirects. The server checks the archive with the checks of ADR 199
   before the configuration version is `uploaded`, and refuses an archive
   that fails. It never changes an archive to make it pass.
4. **A run is a plan job.** `POST /runs` with a speculative configuration
   version starts a job with the operation `plan` on the runner executor
   (ADR 100), with `source.kind` `cli`. The job meets the gates of
   `change.propose` (ADR 224). A refusal ends the run as `errored`, and the
   log names the decision record. The fields `refresh`, `refresh-only`,
   `target-addrs`, `replace-addrs` and `is-destroy` become the fixed
   arguments of the plan step, from an allowlist. The job runs on the pool
   that the workspace names. The TFE agent pool API is not served.
5. **Run variables are secrets.** The server treats each run variable as a
   secret, because the CLI sends no sensitive flag. It seals them in the
   job spec, gives them only to the runner of the claim, and never writes
   them to a log, an audit entry or an answer. The launcher masks them in
   the log.
6. **The log is the log of the plan phase.** `log-read-url` reads the log of
   the plan phase (ADR 102) in chunks, with the token of the CLI and the
   read action of job logs on the unit.
7. **No apply, fail closed.** The server refuses a configuration version
   with `speculative=false` with a JSON:API error that says that the
   server runs speculative plans only and that names `execution-mode`
   `local`. A route of the apply (`actions/apply`, `actions/discard`,
   `actions/cancel` of a run that is not a plan) answers the same error.
   `actions/cancel` of a plan cancels its job (ADR 100 decision 11).
8. **The routes of the poll answer.** `run-events` answers an empty list.
   `include=task_stages` answers no task stage. `runs/queue` and `capacity`
   answer the jobs of the namespace that the caller can read. The server
   never answers 404 for a route that the CLI reads with no tolerance.
9. **Each route has its e2e step first.** The e2e suite drives the real
   OpenTofu CLI with the `cloud` block on a workspace with `execution-mode`
   `agent` and a test runner: `tofu plan` streams the log of the job, and
   `tofu apply` fails with the error of decision 7. The `remote` backend
   waits for its own e2e step.
10. **What RFC 30 needs before an apply.** RFC 30 stays open for the apply.
    Its ADR must decide these points:
    - How `is-confirmable` and `actions/apply` map to the approvals and
      the gates of `change.apply` (ADRs 191 and 225).
    - That the apply phase applies the stored plan of the run.
    - How `-auto-approve` (`auto-apply=true`) meets `autoApply` of the
      decision record (ADR 229 decision 3).
    - The saved cloud plan of `-out`.
    - The `remote` backend. It rejects run variables and `plan -out`,
      and it reads no run-events and no task stages.
    - How runner labels map to agent pools.

Benchmark: CI services with self-hosted runners and the agents of hosted
IaC services run the plan in the network of the customer, and the service
only queues it and streams the log. SOC 2 change management and ISO 27001
change control apply to changes. A speculative plan changes nothing, so it
needs no approval.

## Consequences

- A team runs `tofu plan` on a workstation, and the plan runs with the
  credentials of a runner in its own network. The workstation needs no
  cloud credentials.
- The configuration of a workspace reaches the server as a file set. The
  server seals it and keeps it for the retention of ADR 199. The user
  documentation says this.
- The variables that a person passes on the command line reach the
  server. The server seals them. The user documentation says this.
- `tofu apply` on a workspace with `execution-mode` `agent` fails until the
  apply slice. A team that applies from a workstation keeps the workspace
  `local`.
