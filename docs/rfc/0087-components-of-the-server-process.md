---
rfc: 87
title: Components of the server process, and a start flag that selects them
status: accepted
created: 2026-10-19
adr: 249
---

# RFC 87. Components of the server process, and a start flag that selects them

Status: accepted. The product manager wrote this RFC from the spike
[multi-instance](../spikes/multi-instance.md). The proposals have the label
"(product manager)". On 2026-10-19 the project owner answered the open
questions, as the project manager relayed.
[ADR 249](../adr/0249-components-of-the-server-process.md) records the
decision. No code exists for it yet.

This RFC takes over the open part of
[RFC 7](0007-one-binary-many-roles.md): "the roles of the binary and
`opendustpan serve --role`". [ADR 211](../adr/0211-optional-modules-and-the-capabilities-document.md)
decided the modules and their switches. No ADR on `main` decides how one
process runs only a part of the server.

## Problem

The owner decided on 2026-10-18 that the instances of an installation
scale horizontally (ADRs 215 to 221, RFC 79). The owner also gave this
direction for the process:

- One binary. A start flag selects which parts of the server run in a
  process. The default is all parts.
- An operator scales each part separately.
- No build tags.
- The model is the Temporal server: one binary, the services `frontend`,
  `history`, `matching` and `worker`, and `temporal-server start
  --service <name>`.
- ADR 52 already uses the word "role", so the ADR picks another word.

Today each instance of `opendustpan serve` starts everything: the HTTP
routes, the runner of durable operations, the schedules, the audit
shipper and the hourly audit chain check. An operator cannot run more
HTTP capacity without more background workers, or the opposite. A slow
backup or a long audit chain migration runs in the same process that
answers the Pulumi CLI and OpenTofu.

The spike lists 24 items that break or drift with 2 or more instances
(M1 to M24). The fixes do not need components. With the default "all",
each instance runs every part, and the fixes make that safe. Components
come after the cap of the Helm chart goes (ADR 246 decision 12).

## The word

ADR 52 uses "role" for a named set of actions. The candidates:

| Word | For | Against |
| --- | --- | --- |
| component | Kubernetes uses the label `app.kubernetes.io/component` for this. The chart gets one Deployment for each component with that label. Not in `GLOSSARY.md`. | Generic in prose. |
| service | The word of the Temporal server. | `GLOSSARY.md` has "service token", the principal kind `service`, "service principal" and "hosted service". A fourth sense causes errors. |
| duty | Short, not taken. | No other project uses it, so operators must learn it. |
| workload | Kubernetes word. | The principal kind `workload` and workload identity (ADR 54) take it. |

**Proposal (product manager):** "component". `GLOSSARY.md` gets:
"**Component**: a part of the server that a process runs. One process
runs one or more components. The default is all components. _Avoid_:
role, service, mode."

## Options

1. **Do nothing.** Each instance runs everything. Scale is by instance
   count only. It costs nothing, and it meets the cap work. It does not
   meet the owner's direction to scale each part separately.
2. **Components selected by a start flag, one binary (Temporal model).**
   This is the proposal below.
3. **Separate commands** (`opendustpan serve-api`, `opendustpan worker`).
   Each command has its own flags. A solo operator then runs two commands,
   or a third command runs both. It breaks the rule of ADR 39 decision 1
   that only `serve` opens the database.
4. **Build tags for each component.** The owner ruled this out.

## Proposal (product manager)

The product manager designed the proposal against the Temporal server
model. It does not claim to match Temporal.

### Components

| Component | What it runs | Scales with |
| --- | --- | --- |
| `api` | Every HTTP route: the Pulumi Cloud-compatible API, the TFE-compatible API, the tool-neutral APIs, the installation API, the console files. It starts durable operations (it inserts the row), but it never claims one. | Requests from the CLIs and the console. |
| `worker` | The runner of durable operations (backup, restore, audit chain migration, later jobs and sweeps), the schedule claims, the audit shipper, the audit chain check. | Queue depth and lag of operations. |

Later components, each with its own RFC: a `runner-gateway` for the
runner protocol (RFC 6) when long polls need their own capacity, and the
artifact registry and the upstream proxy of RFC 7.

Each process, whatever its components, runs the parts that every process
needs: the store and its migrations, the key-encryption key check,
`/health/live`, `/health/ready` and the metrics route.

### The flag

- `opendustpan serve --components api,worker`, and the environment
  variable `DUSTPAN_COMPONENTS`.
- The default is `all`. `all` means every component of this binary. So a
  solo operator changes nothing.
- An unknown name stops the start, and the error names the valid
  components.
- Components and module switches are separate. A module switch
  ([ADR 211](../adr/0211-optional-modules-and-the-capabilities-document.md))
  is an installation setting and applies to each component. A component
  is a setting of one process. The capabilities document lists modules,
  not components, and each `api` process answers it the same.
- `opendustpan dev` takes no flag and always runs `all`.
- The start log and the version row of the instance (ADR 72 decision 11)
  record the components. The installation API lists them for each
  instance.
- A process that runs `worker` without `api` still listens on its port,
  for the health and metrics routes only. Each other route answers 404.

### Health and readiness

- `/health/live` stays the same: 200 when the process runs.
- `/health/ready` answers for the components of the process. The process
  is ready when each of its components is ready. The body lists each
  component with its state.
- `api` is ready when the store and the blob store answer, and the audit
  chain gate admits requests (spike item M16).
- `worker` is ready when the store answers and its claim loop ran in the
  last 2 claim intervals. A worker that cannot claim is not ready. A
  rollout then stops, and an alert on unready pods fires. A failed
  readiness probe does not restart a pod. Only the liveness probe does.
  Owner question 8.
- `/health/ready/<component>` answers for one component, so that a probe
  of a process with `all` can check one part.

### Liveness of the work

An `api` process starts an operation and returns. When no `worker` runs,
the operation waits. So:

- The installation API reports each due schedule and each operation that
  no worker claimed for more than 2 lease periods.
- A metric gives the age of the oldest unclaimed operation.
- When no worker claimed work for 2 lease periods, an `api` process
  refuses each request that starts an operation with 503 and the command
  that helps. The owner decided this (question 3, option b).

### Who owns each item of the spike

| Spike item | Owner |
| --- | --- |
| M1, M3 Backup | `api` starts it. `worker` runs it. |
| M2, M4 Restore | `api` starts it. `worker` runs it. |
| M5 Write-path installation check | Each write, in each component |
| M6 Version rows | Each process, with its components |
| M7 Audit chain check and checkpoint signature | `worker` (schedule) |
| M8 OpenTofu state version restore | `api` (request path) |
| M9 Key-encryption key | Each process at start |
| M10 Audit chain start step | Each process at start. The migration that it starts runs on `worker`. |
| M11 Blob store and backup target | Each process at start |
| M12 Audit signing key | `worker` signs. Each process checks the key ID at start |
| M13, M15 Audit sinks | `worker` |
| M14, M18 Settings drift | Each process at start |
| M16 Readiness | Each component |
| M17 Inventory retention sweep | `worker` (schedule). `api` starts a sweep on request as an operation. This changes the route from an inline sweep to a durable operation (`internal/api/server/deleted.go:97` runs it inline today). |
| M19 Pulumi blob orphan sweep | `api` (request path) |
| M20 Ticker fallback | `worker`. A process with `worker` refuses to start without a durable store |
| M21 Sign-in throttle | `api` |
| M22 Audit chain gauge | `worker` writes the check result. Each process reads it at scrape time |
| M23 Pulumi update lease expiry, session expiry | `api` (request path) |
| M24 Bootstrap admin tokens | Each process at start |
| S1 Migrations | Each process at start |
| S2 Schedules and operations | `worker` |
| S3 Audit chain migration | `worker` |
| S4 Audit shipper | `worker` |
| S5, S6, S8 Sign-in, sessions, temporary admin | `api`. The temporary admin sweep is a `worker` schedule |

### The Helm chart

ADR 246 has one Deployment. The proposal for the chart, after the cap
goes:

- `components.split: false` by default: one Deployment, `all`.
- `components.split: true`: one Deployment for each component, each with
  its own `replicas`, resources and `app.kubernetes.io/component` label.
  Only the `api` Deployment gets the Service and the Ingress.
- The `worker` Deployment uses `Recreate` or `RollingUpdate` by the same
  rule as ADR 246 decision 13.

## Open questions

The owner answered each question on 2026-10-19.

1. **The word.** "component". ADR 52 keeps "role" for a set of actions.
2. **The first components.** Two components for now: `api` and `worker`.
   A schedule claim stays in `worker`.
3. **An installation with no worker.** Option b. When no `worker` claimed
   work for 2 lease periods, an `api` process refuses each request that
   starts an operation (for example a backup or a restore) with 503 and
   the command that helps. Other requests stay as they are.
4. **Migrations.** Each process migrates at start, under the advisory
   lock, whatever its components. The start order does not matter.
5. **Configuration that applies to one component.** A process ignores a
   flag that no selected component reads, and logs at start that the flag
   has no effect. The flag does not stop the start.
6. **Does a lease partition stop writes?** Moot. ADR 248 has no home lease
   and no control home, and a partition does not stop writes.
7. **Which processes renew the home lease?** Moot. There is no home lease
   (ADR 248). This RFC no longer has the sentence on the renewal of the
   home lease under "Components".
8. **Liveness of `worker`.** No. `/health/live` does not check the claim
   loop, and stays the same for each component. Readiness and the metric
   of the age of the oldest unclaimed operation report a stuck worker.

## Outcome

Accepted on 2026-10-19 by the project owner, as the project manager
relayed: [ADR 249](../adr/0249-components-of-the-server-process.md). The
word is "component". The first components are `api` and `worker`, and
`opendustpan serve --components` selects them, with `all` as the default.
The open questions above hold each answer. The Outcome of
[RFC 7](0007-one-binary-many-roles.md) links ADR 249.
