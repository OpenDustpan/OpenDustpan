# 101. Two executors: write-back and runners

Status: accepted · 2026-10-17 · decision 10 superseded in part by ADR 133

## Context

ADR 100 defines jobs, phases and their states. A job needs an executor:
something that runs its phases and reports back. The project owner decided
that two executors come in parallel on the one job model:

- **Write-back.** OpenDustpan writes a change to git, as a direct push or a
  review request, and the team's existing CI applies it. OpenDustpan
  approvals add to the approvals of the forge. They never gate a team that
  runs only in its own CI.
- **Runners.** The runner is shell-free: its launcher starts each step as
  an argument list. Runners run on Linux only. A runner runs one job at a
  time by default. The owner decided the protocol, for a later pull request:
  `/runner/v1`, HTTP and JSON, long polling, leases (`SKIP LOCKED`), and
  two versions of skew.
- **No credential in a variable.** The job token and the secrets of a
  profile never pass through an environment variable.

[RFC 6](../rfc/0006-runners-and-remote-runs.md) proposes runners, labels and
job steps. [RFC 12](../rfc/0012-api-edits-and-write-back.md) proposes the
`Forge` port and write-back through the API of the forge.

The owner approved the decisions of this ADR on 17 October 2026.

## Decision

1. **One executor port.** `job.Executor` has three methods:
   - `Kind` names the executor: `write-back` or `runner`.
   - `Phases` checks a spec and returns its phases. It returns
     `ErrUnsupported` for an operation that the executor cannot do, and
     `ErrInvalid` for a spec that it cannot run: edits that fail
     `job.ValidateEdits`, steps that fail `job.ValidateSteps`, a pool of
     another namespace than the unit, or no profile.
   - `Abandon` undoes what a job left open after a cancel, a rejection or an
     expiry. Write-back closes its review request when no merge happened. A
     runner has nothing to undo. A durable operation of the kind
     `job.abandon` calls it (ADR 100 decision 12), so it retries with the
     backoff of the durable runner and survives a restart.
2. **Two progress ports that both executors call.** The job core implements
   them on the durable store and the job store:
   - `job.Claimer`: `Claim`, `Renew`, `Release` and `Finish`.
   - `job.Recorder`: `BeginStep`, `EndStep`, `AppendLog`, `PutPlan` and
     `Delivered`. ADR 102 gives the rules of `AppendLog` and `PutPlan`.

   Every call carries a `job.PhaseClaim`: the job, the phase index, the
   durable claim and the namespace fence that the claim took. The store
   checks the token of the claim and the fence in the transaction of each
   write. The write-back executor calls these ports in the server. The
   runner protocol calls them for each runner. So both executors meet the
   same rules for claims, cancel, steps, logs and plans. A claim gives the
   job and its plans. It gives no list of done steps: a new claim runs the
   whole phase on a fresh checkout (ADR 100 decision 9).
3. **Write-back runs in the server.** The deliver phase is an operation of
   the kind `job.write-back` that a `durable.Runner` of each instance
   claims. Its steps use the `Forge` port of RFC 12, which a later ADR
   decides:
   - Read each file at the base commit, and change only the keys of the
     edits. A key that changed after the base fails the job, and the console
     shows both values. OpenDustpan never overwrites without a check.
   - Record the start of the step `commit` through the claim, then commit
     through the API of the forge, never with a git client. Each commit
     has the trailer `Dustpan-Job: <job ID>`.
   - **Every takeover looks first.** On any claim after the first
     (`Phase.Claims` above 1), and on any claim when a claim recorded the
     start of `commit`, the executor looks for the commit of the job
     before it commits (`job.NeedsCommitLookup`). It does this whatever the
     earlier claim recorded, because the forge can make the commit before
     the job store has the start record, or after the executor lost its
     claim. For a direct
     push, it looks for a commit with the trailer of the job on the target
     ref. For a review request, it looks for the branch `dustpan/<job ID>`
     and the commit with the trailer on it, and for the open review
     request of that branch. When it finds the commit, it records it and
     makes no second commit. So a change never lands twice.
   - For a review request, commit to the branch `dustpan/<job ID>` and open
     the review request.
   - Record the commit, the branch and the review request with
     `Recorder.Delivered`. The delivery time is the time that the forge
     gives for the commit, also for a commit that a lookup found. The job
     then waits in `awaiting-state`, or closes at once when the job store
     already knows a final state version after that time (ADR 100
     decision 15).

   A forge error that can pass, such as a timeout or a rate limit, retries
   with the backoff of the durable runner. A conflict or a refusal by
   branch protection fails the job at once.
4. **Write-back edits only the config of the unit.** `job.ValidateEdits`
   checks each edit at start:
   - The path has the rules of a step directory: relative, clean, in the
     directory of the code location.
   - The path is a config or variable file of the unit, as the
     `StepPlanner` of its tool tells with `Editable`: for example an
     OpenTofu `.tfvars.json` file or a Pulumi stack settings file.
   - No part of the path, or of the directory of the code location, is CI
     configuration, such as `.github`, `.gitlab-ci.yml` or `Jenkinsfile`,
     whatever the tool says.
   - 1 to 64 edits, 1 to 256 keys for each edit, and 256 KiB of values.
5. **Write-back does `apply` only.** A write-back job changes data files.
   The team's CI decides what to run. A write-back of `plan`, `destroy` or
   `refresh` fails with `ErrUnsupported`.
6. **The team's CI closes a write-back job.** OpenDustpan schedules nothing
   in the CI of the team. It does not ask the CI for a status. The next
   final state version of the unit closes the job, after the merge for a
   review request (ADR 100 decision 15). A job that sees no such version
   expires after 14 days by default (`job.DefaultStateTimeout`).
7. **A runner is shell-free.** A step is an argument list. The launcher of
   the runner starts `Args[0]` with `Args[1:]` directly, with no shell.
   There is no expansion, no quoting and no pipe. `Args[0]` is a bare
   program name that the launcher finds on its `PATH`, or a clean absolute
   path. A team that needs a pipe writes a script in its repository and
   names the script as a step. `job.ValidateSteps` checks each step at
   start:
   - 1 to 64 steps, with names of lowercase letters, digits and `-`, unique
     in the job.
   - 1 to 256 arguments for each step, and 128 KiB for the arguments of all
     steps. No NUL byte.
   - Variable names that are valid, and that do not start with `DUSTPAN_`.
     The runner sets those variables, for example the job ID.
   - No variable that the **credential-variable rule** (`job.IsCredentialEnv`)
     matches: a name in a fixed list (`PULUMI_ACCESS_TOKEN`,
     `PULUMI_CONFIG_PASSPHRASE`, `TF_HTTP_PASSWORD`, `AWS_SECRET_ACCESS_KEY`,
     `AWS_SESSION_TOKEN`, `ARM_CLIENT_SECRET`, `GOOGLE_CREDENTIALS` and
     others), a name that starts with `TF_TOKEN_`, or a name that ends with
     `_TOKEN`, `_SECRET`, `_PASSWORD`, `_PASSPHRASE`, `_SECRET_KEY`,
     `_ACCESS_KEY`, `_PRIVATE_KEY`, `_CREDENTIALS` or `_API_KEY`. The match
     ignores case.
   - A directory and output paths that stay in the code directory.
   - A timeout from 0 to 24 hours. 0 takes 1 hour.
   - No step that changes resources in the plan phase.
8. **The server decides the steps and what they change.** Each tool has a
   `job.StepPlanner`, which gives the default steps of each operation and
   tells which files a write-back can edit. Its adapter lives in the
   internal wiring of the server, next to the tool package, so a tool
   package never imports `job`. The adapter marks which of its steps change
   resources. Custom steps come only from a blueprint or from the settings
   of the unit. A change to them needs its own action, `unit:define-steps`.
   For custom steps, `job.MarkCustomSteps` marks every step of the apply
   phase as one that changes resources, whatever the author wrote. A start
   request has no steps, so no caller can replace the steps or say what
   they change. The job core never knows a command of a tool.
9. **A step that changes resources says so.** `Step.ChangesResources`
   marks it, as decision 8 sets it. The runner records the start of such a
   step before the launcher starts the process (ADR 100 decision 9).
10. **No credential in a variable.** The job record holds no secret. The
    secrets come from the profile of the spec when the runner claims the
    phase, through the runner protocol. The runner masks them in the log.
    They reach the tool this way:
    - **OpenTofu.** The job token (ADR 100 decision 20) reaches OpenTofu
      through a credentials helper, which is the runner's own binary. The
      runner writes a CLI configuration that names the helper, and the
      helper answers the token for the host of the server.
    - **Pulumi.** The runner writes the job token in a file of mode 0600 on
      a memory-backed volume, as the credentials file of a Pulumi home
      directory for the step. The secrets of the profile go to files of
      mode 0600 on that volume too, for example a shared credentials file of
      a cloud provider.
    - Only the steps of the role `tool` get the files or the helper. A
      `hook` step before the tool never does. The runner removes the files
      when the step ends.
    - The plan phase gets read-only credentials where the profile has them.
11. **Plans and outputs.** A step can name output files: `plan-display` (the
    readable JSON plan) and `plan-apply` (the plan that the apply phase
    reads, for example an OpenTofu plan file). They are the two parts of
    the one plan of the phase. The runner uploads both with one
    `Recorder.PutPlan` call after the last step that names an output
    (ADR 102).
12. **Linux, one job at a time.** A runner runs on Linux. It runs one phase
    at a time by default. Its launcher starts each step in its own process
    group. A later setting may allow more than one phase per runner.
13. **Cancel and timeouts on a runner.** At a cancel or a step timeout, the
    runner sends `SIGINT` to the process group of the step. The Pulumi CLI
    and OpenTofu stop at a safe point on `SIGINT` and save their state.
    After a grace period, the runner sends `SIGKILL`. The grace period is 10
    minutes for a step that changes resources, and 10 seconds for other
    steps. The runner then ends the phase with `Claimer.Finish`. A phase
    that fails during a cancel ends the job `failed` (ADR 100 decision 7).
14. **Runner pools.** A runner pool belongs to a namespace. A runner belongs
    to one pool and claims only the kind `job.runner.<pool ID>`. A runner
    job names its pool at start, and the pool must belong to the namespace
    of the unit. So a runner of one namespace never runs a job of another.
    Labels inside a pool, as in RFC 6, are a later step (see "Open
    questions").
15. **The runner protocol is for a later pull request.** Its shape is
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
- A provider that reads a credential only from an environment variable does
  not work on a runner until it can read a file. The credential-variable
  rule refuses a step that sets such a variable.
- Every custom step of the apply phase counts as a change. A takeover after
  a custom hook in the apply phase began fails the job, also when the hook
  changed nothing.

## Open questions

- Labels: does a job ask for labels inside a pool, as in RFC 6? Exact match
  on a pool works with the durable claim today. A label match needs a
  change to the claim query of the durable port.
- Does a namespace policy refuse `sh`, `bash` and other shells as `Args[0]`?
- Does a runner run each phase in a new container? RFC 6 asks this too.
- **For the runner protocol pull request:** runner credentials. Is a runner
  a service principal of the namespace with `runner:claim` on its pool, or
  a token kind of its own (RFC 6)?
- **For the runner protocol pull request:** the `Forge` port. Which forges
  come first, and which methods does the port need (RFC 12)? The review
  watcher needs webhooks for merge and close, and a read of a review
  request for its polling fallback.
- Where does the console show a review request that closed without a merge
  after the job expired?
