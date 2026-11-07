# 343. Runner work: tool imports, sweeps and the runner repository

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 241 decisions 2 and 3: an import that needs the CLI of a tool is a runner job.
- Changes ADR 130 decision 3: a runner pool may have more than one purpose.
- Changes ADR 164 decision 5: a sweep pool no longer runs only sweeps.
- Extends ADR 164 decision 9: converters run on a pool that has the sweep purpose.
- Extends ADR 282 decision 4: the runner repository is an exception for the runner only.

## Context

ADR 241 decision 2 says that an import runs in the server. Decision 3 lets a workstation read a source with the CLI of the tool and upload each version. ADR 241 rejects a job on a runner. Its reason: a source that is an HTTP API or an object store needs no CLI.

ADR 130 decision 3 gives a runner pool one purpose, `jobs` or `sweeps`. The purpose never changes. ADR 164 decision 5 says that a sweep pool runs only sweeps. ADR 164 decision 9 leaves open if OpenDustpan ships converters from a cloud inventory service to the sweep report format.

ADR 282 decision 4 puts the runner in its own repository, `OpenDustpan/dustpan-runner`. ADR 8 splits the server and the console. Each repository has its own CI and releases.

On 7 November 2026 the owner decided the points below.

## Decision

Decided by the project owner on 7 November 2026.

1. **A tool import is a runner job (owner, 2026-11-07).** An import that needs the CLI of a tool runs as a runner job. Examples are `tofu state pull` and `pulumi stack export`. The CLI does not run in the server.
2. **A bulk import runs in the server (owner, 2026-11-07).** A bulk import reads another backend directly. It keeps the rule of ADR 241 decision 2: it runs in the server, in durable operations.
3. **Sweeps run on a runner (owner, 2026-11-07).** ADR 164 decision 1 stays.
4. **A pool may have more than one purpose (owner, 2026-11-07).**
5. **Converters (owner, 2026-11-07).** A converter from a cloud inventory service to the sweep report format runs on a runner pool that has the sweep purpose.
6. **The runner repository is an exception for the runner only (owner, 2026-11-07).** The runner lives in its own repository, as ADR 282 decision 4 says. No other component gets a repository of its own from this exception.

## Consequences

- This ADR does not decide how a client starts a tool import. It does not decide which pool the job uses. It does not decide what happens to `opendustpan import upload` (ADR 241 decision 3).
- This ADR does not decide if the purposes of a pool can change after its creation. ADR 130 decision 3 says that they cannot.
- ADR 164 decision 5 gave a reason. The identity of a host is the credential. A pool that applies changes has an identity that can write. A pool with both purposes has such an identity. This ADR does not decide how a sweep stays read-only on such a pool.
- This ADR does not decide the API values for several purposes. It does not decide the check in `ScanStore.CreateTarget` and `UpdateTarget` for a pool that has the sweep purpose.
- This ADR does not decide who writes or ships the converters. It decides only where they run.
- A page of the user documentation for runner pools, sweeps and import needs an update when the feature lands.
