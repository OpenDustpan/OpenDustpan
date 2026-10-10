---
rfc: 6
title: Runners and remote runs
status: draft
created: 2026-10-10
---

# RFC 6. Runners and remote runs

Status: draft. The names and the scope are proposals.

## Problem

Today every Pulumi update and every OpenTofu run starts on a workstation or in
the team's own CI. The backend sees the result, not the request. Teams then
ask for things that need something to start a tool for them:

- a plan or preview for each pull request, with the result in the console,
- an apply after a merge, with an approval step,
- a scheduled refresh that reports drift,
- a unit that destroys itself after a set time.

Pulumi Deployments, HCP Terraform runs (with agents in the customer's
network), GitLab runners and Temporal workers all solve this.

## Principle

**The backend never runs an infrastructure-as-code tool.** The customer hosts
the processes that do. This keeps cloud credentials, source code and tool
binaries out of the server. It also keeps the server small: no sandboxes, no
tool installs, no build caches.

This principle is the first thing an accepted ADR must record.

## Proposal

- A **runner** is a process that the customer hosts. It asks the backend for
  work, runs the tool's CLI in the customer's network, and reports back.
- A **job** is one piece of work for a runner: a tool, a unit reference
  (ADR 13), an operation (Pulumi `preview`, `up`, `refresh`, `destroy`;
  OpenTofu `plan`, `apply`) and the source to run it on. A job produces a
  Pulumi update or an OpenTofu run, in each tool's own words.
- **Pull, never push.** A runner opens every connection. It long-polls the
  backend for jobs, so no one opens a port into the runner's network.
- **Registration.** A runner registers with a runner token that belongs to
  one namespace. The token can only claim jobs and report on them. It cannot
  read units or profiles beyond what a claimed job needs.
- **Labels.** A runner has labels (`aws-prod`, `eu-west-1`). A job asks for
  labels. Only a runner with all the labels can claim it, as with GitLab
  runner tags and Temporal task queues.
- **Claims are leases.** A runner claims a job with a lease that expires,
  and renews it while it works. If the runner stops, the lease expires and
  the job can go to another runner. The claim is a write in the namespace, so
  it goes through the fence (ADR 4).
- **The tool talks to the backend as usual.** On the runner, the Pulumi CLI
  logs in to the backend and runs `pulumi up` like on a workstation. The
  update, its events and its checkpoints arrive through the Pulumi
  Cloud-compatible API. The job only links to the update. For OpenTofu, the
  runner runs `tofu` with the `cloud` block, and state versions arrive through
  the TFE-compatible API.
- **Source.** The runner checks out the source itself (a git URL and a
  commit). The backend never stores the code. Remote runs need a forge
  connection anyway (RFC 12): webhooks start jobs, and the backend posts
  statuses and plan results on review requests. So the backend holds the
  forge app's credentials. It can give a runner a short-lived, read-only
  token for one repository. A runner can also use git credentials of its
  own.
- **Values.** The runner opens the job's profile (RFC 5) and passes the
  values to the tool. Secrets and short-lived credentials go only to the
  runner that claimed the job.
- **Logs.** The runner streams the tool's output to the backend while the job
  runs, so the console can show it live.

What starts a job comes later, one trigger at a time: a request from the
console or the API, a git webhook, a schedule.

## Job steps

Teams run more than the tool: `terragrunt`, `terramate`, `atmos`, a version
manager such as `tenv`, a script that makes files, a check against an
internal API. A job is a list of steps, not one command.

- **Hooks.** Commands that run at fixed points: before init, after init,
  before the plan or preview, after it, before the apply or update, after it.
  A hook that fails stops the job, unless the hook is marked as allowed to
  fail.
- **A wrapper in place of the tool.** The job can run a wrapper command in
  place of `tofu` or `pulumi`, for example `terragrunt plan`. The wrapper must
  still use OpenDustpan as the backend: for Terragrunt, a `generate` block
  writes a `cloud` block for the TFE-compatible API.
- **One job, one unit.** A job produces one Pulumi update or one OpenTofu run.
  `terragrunt run-all` changes many workspaces in one command, so it does not
  fit one job. It needs one job for each Terragrunt unit, or a job kind that
  produces many runs.
- **Tools on the runner.** The runner does not download tools. The customer
  puts them on the runner, or in the container image that the runner starts
  for each job.
- **Where steps are defined.** In a file in the source, like a CI file, or in
  the job settings in OpenDustpan, or in a blueprint (RFC 14). A file in the
  source lets anyone who can open a review request change the steps.
- **Logs and secrets.** Step output goes to the job log like the tool's
  output. The runner masks values that it got as secrets from a profile.

A plan already runs code: a Pulumi preview runs the program, and an OpenTofu
plan runs providers and `external` data sources. So hooks add little new
risk. The rule that matters is older than hooks: never run a job for a review
request from an untrusted fork with credentials that can change production.

## Options

1. **Runners as above.**
2. **Do nothing.** Teams keep running tools in their own CI. The console shows
   results only. This is the current state and stays valid.
3. **Hosted execution in the backend.** This breaks the principle above. It is
   listed only to record that it was considered and rejected.

## Effect on RFC 1

RFC 1 serves OpenTofu for local execution only: `operations: false`, no TFE
runs. With runners, the server could serve TFE remote execution: the CLI asks
for a run, a runner does the work, and the server streams the logs back. Like
an HCP Terraform agent, the runner does the work, and the backend only queues
it. This puts a large part of the TFE run API in scope. It belongs in RFC 1,
or in a follow-up to it, after this RFC is accepted.

## Proposed glossary entries

**Runner**:
A process that the customer hosts. It claims jobs from the backend and runs
infrastructure-as-code tools in the customer's network.
_Avoid_: Agent, worker, executor

**Job**:
One piece of work for a runner. It produces one Pulumi update or one OpenTofu
run.
_Avoid_: Task, deployment, run (without a tool)

**Remote run**:
The feature: a job that a runner does, started from the backend.
_Avoid_: Deployment, hosted run

"Agent" stays reserved for AI coding agents in this repository.

## Open questions

- Does a runner run each job in a fresh container (like GitLab's Docker
  executor), or directly on its host? A container gives each job a clean
  set of tools and files.
- Does `terragrunt run-all` get a job kind that produces many runs?
- Is a job a domain package of its own (`job`), next to `stack` and
  `workspace`? It refers to units of both tools through unit references.
- Which first trigger: the console, the API or a git webhook?
- Who can approve an apply, and does approval need roles from RFC 2 first?
- Do runner tokens come from the access tokens of ADR 6, or are they a kind of
  their own?
- How long does the backend keep job logs, and do logs ever hold secrets the
  tool printed?
- Does the Pulumi CLI's own "deployments" API matter, or do runners only use
  the update API?

## Gaps from the competitor review

Added 2026-10-10 from the research doc "IaC and TACOS landscape". Not yet
worked into the proposal above.

- Log secret masking, such as `::add-mask` (Spacelift).
- Run timeouts, a kill after no log output, expiry of queued jobs (Spacelift,
  Pulumi).
- Retry with the same parameters, run-scoped variables, a debug-log toggle
  (Scalr).
- Run priority, serialization by a key, pause per unit or namespace
  (Spacelift, Harness, Pulumi).
- Run options: refresh-only, replace or target resources, allow empty apply
  (HCP Terraform, Spacelift).
- Jobs for import, `state rm` and `state mv` (Atlantis).
- Hooks on success and on failure, and a step that asks for approval (env
  zero).
- Scanner results (Checkov, TFLint) shown with the run, not only in the log
  (Scalr, Harness).
- A tool version pin per unit, and standard run variables such as run ID and
  commit (Burrito, HCP Terraform).
- Ephemeral runners: one Kubernetes job per run (Terrakube); see RFC 31.
- A global switch that stops applies (Atlantis); see RFC 27.

## Outcome

None yet.
