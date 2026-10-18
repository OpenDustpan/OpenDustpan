# 130. Runner pools, runners and coordinators

Status: accepted · 2026-10-18 · adds to ADR 101 decisions 12 and 14

## Context

[ADR 101](0101-two-executors-write-back-and-runners.md) decides that a
runner pool belongs to a namespace, that a runner belongs to one pool, and
that a runner runs one phase at a time by default. It leaves the shape of
pools, runners and the places where steps run to the runner design.

ADR 164 (a draft on another branch)
asks that a pool get a purpose that cannot change after its creation, and
that a pool for sweeps carry a read-only identity.

The project owner decided on 17 October 2026:

- The launcher is a static binary that runs steps as argument lists. The
  image of the team holds only the tools. It is a separate binary in the
  same repository. It runs on Linux only, on any `GOARCH`. The tests of a
  release build run on `amd64` and `arm64`.
- A runner runs one job at a time by default.
- On Kubernetes, a coordinator creates one Kubernetes Job for each job
  phase, with `backoffLimit: 0`.
- On machines, a static coordinator starts a Firecracker microVM, which
  needs `/dev/kvm`, a gVisor container, or a sandboxed process. The
  sandboxed process is weaker and is for tests. A cloud VM for each job is
  shelved.
- Pools belong to a namespace. A pool has a fixed purpose: jobs or
  sweeps. A pool for sweeps carries a read-only identity.

The project owner approved every decision of this ADR. The label "(owner)"
names a decision that the text of the owner of 17 October 2026 states.
The ports are in `pkg/runner`.

## Decision

1. **A runner is a coordinator (owner, for the two kinds).** A runner is a
   long-lived process that the team hosts. It registers in one pool, claims
   work for the pool, and starts a launcher for each claim in an isolated
   place. A runner never runs a step itself. There are two kinds
   (`runner.Kind`):
   - `kubernetes`: one Kubernetes Job for each claim (ADR 136).
   - `machine`: one sandbox for each claim on its own host (ADR 137).

   The **launcher** runs the steps of one claim inside the Job or the
   sandbox (ADR 134). The image of the team holds the tools. The launcher
   binary comes from an image of OpenDustpan, or from the host of the
   machine coordinator.
2. **The isolation of a runner.** A runner joins with one isolation kind
   (`runner.Isolation`): `kubernetes`, `firecracker`, `gvisor` or
   `process`. A Kubernetes runner uses `kubernetes`. A machine runner uses
   one of the other three. The runtime class of the pod template can add
   gVisor on Kubernetes. A pool lists the isolation kinds that its runners
   may use. An empty list allows every kind except `process`. A pool must
   list `process` by name to allow it, and the console shows a warning on
   such a pool.
3. **A pool has a purpose that never changes (owner).** The purpose is
   `jobs` or `sweeps` (`runner.Purpose`). The server takes the durable kinds
   that a runner claims from the pool of the runner, never from the runner
   (`runner.ClaimKinds`):
   - A runner of a pool for jobs claims only `job.runner.<pool ID>`.
   - A runner of a pool for sweeps claims only `unmanaged.sweep.<pool ID>`.

   A change of the purpose fails with `runner.ErrImmutable`
   (`runner.CheckUpdate`). A team that wants the other purpose creates a
   second pool. A runner job that names a pool for sweeps fails at start
   with `runner.ErrPurpose` (`runner.CheckJobPool`). This adds to ADR 101
   decision 14.
4. **The word (owner).** The owner's text says "sweeps", and this design
   uses that word for the work that finds unmanaged resources:
   - the purpose value in the API is `sweeps`,
   - the durable kind of a sweep starts with `unmanaged.sweep.`,
   - the launcher gives the path of the report in `DUSTPAN_SWEEP_REPORT`,
   - the console labels the purpose "Sweeps".

   The glossary already has the **retention sweep** for the purge of
   deleted units. The glossary entry of the new term tells the two apart
   (see the pull request).
5. **A pool for sweeps is read-only (owner).** A pool for sweeps carries a
   read-only identity: the workload identity of its hosts, or the read-only
   entry of a profile (ADR 164 decision 4). `Pool.ReadOnly` is true for
   such a pool. The server cannot check the role of a host. The console and
   the guide say so on each pool for sweeps, and give the read-only role of
   each cloud.
6. **One claim at a time by default (owner).** A pool has a concurrency
   from 1 to 64 (`Pool.Concurrency`). The default is 1. Each runner of the
   pool holds at most that many claims. A Kubernetes runner usually needs a
   higher value, because each claim is a Job of its own, not a process on
   the runner.
7. **The lease of a pool.** Each claim of a runner of the pool has the
   lease of the pool: 90 seconds by default, from 30 seconds to 10 minutes
   (`runner.DefaultLease`). The holder renews after a third of the lease
   (`runner.RenewEvery`). So one lost renewal does not lose the claim. This
   answers the question of ADR 100 about the default lease of a runner
   claim: 30 seconds is too short for a runner on a slow network.
8. **Limits.** A namespace has at most 50 pools, a pool at most 1,000
   runners that are not retired, and at most 100 open join tokens. A runner
   has at most 32 labels. Labels are text for people. A job does not match
   on labels (see "Open questions"). Installation settings can change these
   limits later.
9. **The status of a runner.** The server derives the status from the last
   request and the key of the runner (`Runner.StatusAt`):
   - `online`: a heartbeat in the last 2 minutes. A runner sends one every
     15 seconds.
   - `offline`: no heartbeat in the last 2 minutes.
   - `draining`: an administrator asked the runner to claim no new work.
   - `must-rejoin`: the key of the runner is older than 30 days (ADR 133).
   - `retired`: an administrator retired the runner. Its key authenticates
     nothing, and the record stays for the audit log.
10. **A delete of a pool in use fails.** A delete of a pool fails with
    `runner.ErrPoolInUse` while a job that is not terminal or a sweep target
    names it. A delete of a pool also deletes its runners and its join
    tokens.
11. **Git credentials of a step.** A pool sets what a step gets when its
    tool runs git, for example to get an OpenTofu module
    (`runner.GitCredentials`):
    - `none`: no git credential. This is the default.
    - `forge`: the launcher is the git credential helper of the step. It
      answers a short-lived read-only token of the forge connection of the
      job, for the hosts of that connection only.
    - `custom`: a helper program of the team, in the image. It is a bare
      `git-credential-` name or a clean absolute path. Git starts a helper
      that starts with `!` through a shell, so the server refuses that
      form.
12. **Ports.** `runner.PoolStore` holds pools and join tokens,
    `runner.MemberStore` holds runners and their keys, and
    `runner.SessionStore` holds handoff tokens, job sessions and the nonces
    of proofs (ADR 133). `runner.SweepClaimer` claims, renews and releases
    the sweeps of a pool for sweeps, and the upload of the report ends the
    claim (ADR 131). The suite
    `pkg/runner/runnertest` states their behavior. A reference adapter in
    memory, in the tests of that package, runs each case today. Pools,
    runners and join tokens are namespace data. Each write carries the
    fence and appends its audit entries in its transaction. Handoff tokens
    and sessions are control data that live as long as a claim.

## Consequences

- A team runs a coordinator, never a process that runs steps with its own
  credentials. A step never shares a file system or a process space with
  the coordinator or with another claim.
- A takeover of a claim starts a new Job or a new sandbox. Kubernetes and
  the sandbox never start a launcher of one claim twice.
- A team that wants sweeps and jobs on one cluster runs two pools, each with
  its own service accounts. The purpose cannot drift.
- The console needs a pool page with its runners, their status and the
  command to join again (ADR 133).
- `GLOSSARY.md` needs the terms Coordinator, Isolation, Pool purpose,
  Sweep, Join token, Runner key, Handoff token, Job session, Handoff, Source
  snapshot and Source policy (see the pull request).

## Open questions

- Labels inside a pool, as in RFC 6. Exact match on a pool works with the
  durable claim today. A match on labels needs a change to the claim query
  of the durable port.
- Does a pool for jobs of production need its own namespace policy, for
  example a rule that only a pool with `firecracker` or `kubernetes`
  isolation runs an apply?
- Does a runner report its load (free CPU, free memory) so that the server
  can prefer a runner with room? One claim at a time makes this less
  urgent.
