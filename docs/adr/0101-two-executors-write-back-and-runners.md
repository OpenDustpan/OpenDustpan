# 101. Two executors: write-back and runners

Status: accepted · 2026-10-17

## Context

ADR 100 defines jobs, phases and their states. A job needs an executor:
something that runs its phases and reports back. The project owner decided
that two executors come in parallel on the one job model:

- **Write-back.** OpenDustpan writes a change to git, as a direct push or a
  review request, and the team's existing CI applies it. OpenDustpan
  approvals add to the approvals of the forge. They never gate a team that
  runs only in its own CI.
- **Runners.** The runner is shell-free: a static executor runs each step
  as an argument list. Runners run on Linux only. A runner runs one job at a
  time by default. The owner decided the protocol, for a later pull request:
  `/runner/v1`, HTTP and JSON, long polling, leases (`SKIP LOCKED`), and
  two versions of skew.

[RFC 6](../rfc/0006-runners-and-remote-runs.md) proposes runners, labels and
job steps. [RFC 12](../rfc/0012-api-edits-and-write-back.md) proposes the
`Forge` port and write-back through the API of the forge.

This ADR records the design for review. The owner decisions above are the
owner's. The other decisions take effect when the project lead accepts the
pull request that adds this ADR.

## Decision

1. **One executor port.** `job.Executor` has three methods:
   - `Kind` names the executor: `write-back` or `runner`.
   - `Phases` checks a spec and returns its phases. It returns
     `ErrUnsupported` for an operation that the executor cannot do.
   - `Abandon` undoes what a job left open after a cancel, a rejection or an
     expiry. Write-back closes its review request when no merge happened. A
     runner has nothing to undo.
2. **Two progress ports that both executors call.** The job core implements
   them on the durable store and the job store:
   - `job.Claimer`: `Claim`, `Renew`, `Release` and `Finish`.
   - `job.Recorder`: `BeginStep`, `EndStep`, `AppendLog`, `PutPlan` and
     `Delivered`.

   Every call carries a `job.PhaseClaim`: the job, the phase index and the
   durable claim. The store checks the token of the claim in the transaction
   of each write. The write-back executor calls these ports in the server.
   The runner protocol calls them for each runner. So both executors meet
   the same rules for claims, cancel, steps, logs and plans.
3. **Write-back runs in the server.** The deliver phase is an operation of
   the kind `job.write-back` that a `durable.Runner` of each instance
   claims. Its steps use the `Forge` port of RFC 12, which a later ADR
   decides:
   - Read each file at the base commit, and change only the keys of the
     edits. A key that changed after the base fails the job, and the console
     shows both values. OpenDustpan never overwrites without a check.
   - Commit through the API of the forge, never with a git client.
   - For a review request, commit to the branch `dustpan/<job ID>` and open
     the review request. A takeover finds the branch by that name and does
     not commit twice.
   - Record the commit, the branch and the review request with
     `Recorder.Delivered`. The job then waits in `awaiting-state`.

   A forge error that can pass, such as a timeout or a rate limit, retries
   with the backoff of the durable runner. A conflict or a refusal by
   branch protection fails the job at once.
4. **Write-back does `apply` only.** A write-back job changes data files.
   The team's CI decides what to run. A write-back of `plan`, `destroy` or
   `refresh` fails with `ErrUnsupported`.
5. **The team's CI closes a write-back job.** OpenDustpan schedules nothing
   in the CI of the team. It does not ask the CI for a status. The next
   state version of the unit closes the job (ADR 100 decision 14). A job
   that sees no state version expires after 14 days by default
   (`job.DefaultStateTimeout`).
6. **A runner is shell-free.** A step is an argument list. The runner starts
   `Args[0]` with `Args[1:]` directly, with no shell. There is no expansion,
   no quoting and no pipe. `Args[0]` is a bare program name that the runner
   finds on its `PATH`, or a clean absolute path. A team that needs a
   pipe writes a script in its repository and names the script as a step.
   `job.ValidateSteps` checks each step at start:
   - 1 to 64 steps, with names of lowercase letters, digits and `-`, unique
     in the job.
   - 1 to 256 arguments for each step, and 128 KiB for the arguments of all
     steps. No NUL byte.
   - Variable names that are valid, and that do not start with `DUSTPAN_`.
     The runner sets those variables, for example the job ID.
   - A directory and output paths that stay in the code directory.
   - A timeout from 0 to 24 hours. 0 takes 1 hour.
   - No step that changes resources in the plan phase.
7. **The job record holds no secret.** The variables of a step are not
   secret. Secret values come from a profile when the runner claims the
   phase, through the runner protocol. The runner masks them in the log.
8. **Steps come from the tool, a blueprint or the unit.** Each tool package
   implements `job.StepPlanner`, which gives the default steps of each
   operation. A blueprint or the settings of the unit can replace them. The
   job core never knows a command of a tool.
9. **A step that changes resources says so.** `Step.ChangesResources` marks
   it. The runner records the start of such a step before it starts the
   process (ADR 100 decision 9). A step that a team marks wrongly as safe
   may run twice after a takeover. The tool's own lock or lease then
   protects the state.
10. **Plans and outputs.** A step can name output files: `plan-display` (the
    readable JSON plan) and `plan-apply` (the plan that the apply phase
    reads, for example an OpenTofu plan file). The runner uploads them with
    `Recorder.PutPlan` after the step (ADR 102).
11. **Linux, one job at a time.** A runner runs on Linux. It runs one phase
    at a time by default. It starts each step in its own process group. A
    later setting may allow more than one phase per runner.
12. **Cancel and timeouts on a runner.** At a cancel or a step timeout, the
    runner sends `SIGINT` to the process group of the step. The Pulumi CLI
    and OpenTofu stop at a safe point on `SIGINT` and save their state.
    After a grace period, the runner sends `SIGKILL`. The grace period is 10
    minutes for a step that changes resources, and 10 seconds for other
    steps. The runner then ends the phase with `Claimer.Finish`.
13. **Runner pools.** A runner pool belongs to a namespace. A runner belongs
    to one pool and claims only the kind `job.runner.<pool ID>`. A runner
    job names its pool at start. Labels inside a pool, as in RFC 6, are a
    later step (see "Open questions").
14. **The runner protocol is for a later pull request.** Its shape is
    decided: `/runner/v1`, HTTP and JSON, long polling of `Claimer.Claim`,
    claims with an expiry, and runners of the two releases before the server
    release work. This ADR fixes only the ports that the protocol calls.

## Consequences

- The write-back executor and the runner protocol share every rule about
  claims, cancel, steps and logs. A difference between them is a bug in one
  adapter, not a second design.
- Write-back needs no runner and no git client. An edit lands while no
  runner is online.
- A team that applies only in its own CI gets write-back and nothing else
  from OpenDustpan. Its CI and its forge approvals do not change.
- Shell-free steps remove the class of injection where a value of a variable
  becomes shell syntax. A step can still start a shell by name. A namespace
  policy may refuse that later.
- A runner needs each tool on its host or in its image. RFC 6 already says
  that the runner does not download tools.

## Open questions

- Labels: does a job ask for labels inside a pool, as in RFC 6? Exact match
  on a pool works with the durable claim today. A label match needs a
  change to the claim query of the durable port.
- Does a namespace policy refuse `sh`, `bash` and other shells as `Args[0]`?
- Does a runner run each phase in a new container? RFC 6 asks this too.
- Runner credentials: a service principal of the namespace with
  `runner:claim` on its pool, or a token kind of its own (RFC 6)?
- Which forges come first, and which methods does the `Forge` port need
  (RFC 12)?
- Where does the console show a review request that closed without a merge
  after the job expired?
