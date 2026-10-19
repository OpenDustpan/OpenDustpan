# 249. Components of the server process, and a start flag that selects them

Status: accepted · 2026-10-19. The project owner decided it. Accepts RFC 87. Accepts the open part of RFC 7 (the roles of the binary). Extends ADR 72 decision 11 and ADR 211.

## Context

Each instance of `opendustpan serve` starts every part of the server: the
HTTP routes, the runner of durable operations, the schedules, the audit
shipper and the audit chain check. An operator cannot add HTTP capacity
without more background workers, or the opposite. A slow backup runs in the
same process that answers the Pulumi CLI and OpenTofu.

The owner gave this direction: one binary, a start flag that selects which
parts of the server run in a process, all parts by default, a separate
scale for each part, and no build tags. ADR 52 uses the word "role" for a
set of actions, so this ADR uses another word. RFC 87 holds the options.
The spike `docs/spikes/multi-instance.md` lists the items that break or
drift with 2 or more instances. On 2026-10-19 the owner answered the open
questions of RFC 87, as the project manager relayed.

## Decision

1. **The word is "component".** A component is a part of the server that
   a process runs. One process runs one or more components.
2. **Two components for now: `api` and `worker`.**
   - `api` runs each HTTP route: the Pulumi Cloud-compatible API, the
     TFE-compatible API, the tool-neutral APIs, the installation API and
     the console files. It starts a durable operation (it inserts the row),
     but it never claims one.
   - `worker` runs the durable operations (backup, restore, audit chain
     migration, and later jobs and sweeps), the schedule claims, the audit
     shipper and the audit chain check.
   A later component gets its own RFC.
3. **The flag.** `opendustpan serve --components api,worker`, with the
   environment variable `DUSTPAN_COMPONENTS`. The default is `all`, which
   means each component of this binary. An unknown name stops the start,
   and the error names the valid components. `opendustpan dev` takes no
   flag and always runs `all`. There are no build tags for components.
4. **Components and modules are separate.** A module switch (ADR 211) is a
   setting of the installation and applies to each component. A component
   is a setting of one process. The capabilities document lists modules,
   not components.
5. **The parts that every process runs.** Each process, whatever its
   components, runs the store, the key-encryption key check,
   `/health/live`, `/health/ready` and the metrics route. A process that
   runs `worker` without `api` listens on its port for the health and
   metrics routes only, and each other route answers 404.
6. **Migrations.** Each process migrates at start, under the advisory
   lock, whatever its components. The start order of the processes does
   not matter.
7. **A flag that no selected component reads.** The process ignores it,
   and logs at start that the flag has no effect. The flag does not stop
   the start.
8. **No worker.** When no `worker` claimed work for 2 lease periods, an
   `api` process refuses each request that starts an operation, for example
   a backup or a restore, with 503 and the command that helps. Other
   requests stay as they are.
9. **Health.** `/health/live` stays the same for each component: 200 when
   the process runs. It does not check the claim loop. `/health/ready`
   answers for the components of the process, and lists each component
   with its state. `/health/ready/<component>` answers for one component.
   `api` is ready when the store and the blob store answer and the audit
   chain gate admits requests. `worker` is ready when the store answers and
   its claim loop ran in the last 2 claim intervals.
10. **A stuck worker.** Readiness and a metric of the age of the oldest
    unclaimed operation report a stuck worker. The installation API
    reports each due schedule and each operation that no worker claimed
    for more than 2 lease periods.
11. **Records.** The start log and the version row of the instance (ADR 72
    decision 11) record the components. The installation API lists them
    for each instance.

## Consequences

- `GLOSSARY.md` gets the entry **Component**.
- The fixes of the spike items do not need components. With `all`, each
  instance runs each component, and the fixes make that safe.
- The Helm chart can get one Deployment for each component after its cap
  of one replica goes (ADR 246 decision 12). Only the `api` Deployment
  gets the Service and the Ingress. That change has its own issue.
- RFC 87 open questions 6 and 7 are moot: ADR 248 has no home lease.
- An installation with only `api` processes refuses the start of new
  operations after 2 lease periods, and the error tells the operator to
  start a `worker`.

## Benchmarks

Designed against these, as a comparison only, never a compliance claim:

- The Temporal server: one binary, the services `frontend`, `history`,
  `matching` and `worker`, and `temporal-server start --service <name>`.
- The Kubernetes label `app.kubernetes.io/component`.
