---
rfc: 63
title: A workspace lock belongs to a principal, not to a client
status: accepted
adr: 44
created: 2026-10-12
---

# RFC 63. A workspace lock belongs to a principal, not to a client

Status: accepted as ADR 44.

## Problem

An OpenTofu workspace lock records the subject of the principal that took it.
The server checks only that subject when a client writes state. Two clients
that sign in with the same token have the same subject. So one client can
write state under the lock of the other.

### What the server does today

These facts are from `main` at e89ab7b. The line numbers in the issue came
from 0e71e4d. They moved since then.

- **Lock.** `POST /api/v2/workspaces/{id}/actions/lock` records the subject
  of the caller as the holder (`internal/api/tfe/locks.go`). The server mints
  a lock ID for each acquisition (`Workspace.lock` in
  `pkg/workspace/workspace.go`). A lock request on a locked workspace answers
  409, also when the caller is the holder.
- **Declare.** `workspace.Service.BeginState` (`pkg/workspace/service.go`)
  checks `heldBy(holder)`, which compares only the subject. It records the
  lock ID of the current lock on the pending version.
- **Finalize.** `FinalizeStateVersion` checks in the same transaction that
  the workspace is still held by the lock ID that the version was declared
  under (`checkFinalizeLock` in the store adapters). So a force-unlock between
  the declare and the upload stops the upload.
- **Unlock.** `Service.Unlock` checks `heldBy(holder)`, so any client with the
  same subject can unlock.
- **Lineage and serial.** A new version needs the lineage of the current
  state and a higher serial, unless the client forces the push (ADR 26,
  decision 3). These checks stop an old write. They do not stop a write from
  a client that read the current state.
- **Subjects.** Each token in `DUSTPAN_ADMIN_TOKEN` signs in as its own
  subject: `admin`, `admin-2`, and so on (`pkg/identity/identity.go`). Two
  clients have the same subject only when they use the same token.

The comment on `Lock.ID` in `pkg/workspace/workspace.go` names the risk.
ADR 25 says: "The protocol carries no lock ID from the client. CI jobs that
share one workspace need distinct principals." ADR 26, decision 6, says:
"The server mints a lock ID for each acquisition. The client never sends
one." Nothing in the server enforces the advice of ADR 25.

### When it goes wrong

Two clients, A and B, use the same token on one workspace:

1. **A run with `-lock=false`.** A holds the lock and runs `tofu apply`. B
   runs `tofu apply -lock=false`. OpenTofu then uses a lock manager that does
   nothing (`clistate.NewNoopLocker`, `internal/command/meta_backend.go`), so
   B writes state with no lock call. The server sees a declare from the
   subject that holds the lock, and accepts it. If B read the current state
   first, the lineage and serial checks pass too.
2. **A stale job after a force-unlock.** A holds the lock and stops
   responding. An operator runs `tofu force-unlock`, and B takes a new lock.
   A wakes up and declares a new version. The declare passes, because A and B
   have one subject, and the version gets the lock ID of B. The finalize
   check passes too.
3. **An unlock from the wrong job.** Any client with the token can call the
   unlock route while A runs. The OpenTofu CLI calls it only for its own
   lock, but a script or another tool can call it.

With distinct tokens, the server refuses all three: the declare and the
unlock answer 409 with "is locked by User" and the holder.

### What the OpenTofu CLI sends

I read the OpenTofu source at v1.11.14, v1.12.7 and v1.13.1, the versions of
the e2e matrix (`e2e/tool.go`). On 2026-10-12, "latest" was 1.13.1. The
functions below are the same in the three versions.

- `cloud` block, `internal/cloud/state.go`:
  - `State.Lock` calls `Workspaces.Lock` with only
    `Reason: "Locked by OpenTofu"`. It keeps the lock ID that OpenTofu makes
    (`statemgr.LockInfo.ID`) in the client and returns it.
  - `State.Unlock` compares the lock ID with its own copy in the client,
    then calls `Workspaces.Unlock(ctx, workspaceID)` with no lock ID.
  - `uploadState` and `uploadStateFallback` send lineage, serial, MD5, force,
    the state and the outputs, and a run ID only when the environment holds `TFE_RUN_ID`.
    They send no lock ID.
- `remote` backend, `internal/backend/remote/backend_state.go`:
  `remoteClient.Lock`, `Unlock` and `Put` do the same as above.
- For `tofu force-unlock`, the CLI expects the lock ID `<organization>/<workspace>`
  and checks it in the client.

So the CLI never sends its lock ID to the server, on a lock, a state write or
an unlock. The server cannot bind a write to a lock ID that the client holds.

## Options

### 1. Do nothing more

Keep the advice of ADR 25 and the comment in the code.

- **Cost:** none.
- **Stops:** nothing.
- **API contract:** no change.

### 2. A warning at start-up

The server writes a warning when it serves the TFE-compatible API with only
one admin token. The warning says that CI jobs that share a workspace need
distinct tokens.

- **Cost:** a few lines in `cmd/opendustpan`, and a test.
- **Stops:** nothing. The server cannot see how many clients share a token.
  An installation with one person and one token gets the warning on each
  start, and an installation with two tokens that one job pool shares gets
  none.
- **API contract:** no change.

### 3. Bind the lock to the lock ID that the CLI sends

The server checks the lock ID of the client on each declare and unlock.

- **Cost:** the CLI does not send a lock ID (see above). This option needs a
  change to OpenTofu, and a field or a header that the TFE-compatible API
  does not have. A server that refuses writes without that field breaks every
  OpenTofu version of the e2e matrix.
- **Stops:** all three cases, but only for a client that sends the field.
- **API contract:** adds a field that no supported CLI sends. A server that
  needs it breaks the contract.

This option is not possible with the supported CLI versions.

### 4. One subject for each client

Each client that writes state signs in with its own token, so each has its
own subject.

- **Today:** an operator can already do this. Each extra token in
  `DUSTPAN_ADMIN_TOKEN` is a distinct administrator. Each token is a full
  administrator, and a change of the list needs a restart.
- **Later:** scoped access tokens (RFC 20) and users (RFC 19) give each CI
  job or runner its own principal, with fewer rights and no restart.
- **Cost now:** documentation only: the TFE API reference, the configuration
  reference and a CI example. Later: the work of RFC 19 and RFC 20, which is
  needed for other reasons.
- **Stops:** all three cases when clients do not share a token. It does not
  stop clients that share one token, for example jobs of one CI pool with one
  secret.
- **API contract:** no change. The 409 detail already names the holder.

### 5. Options 2 and 4 together

Document option 4 now, and add the warning of option 2.

- **Cost:** the sum of both.
- **Stops:** the same as option 4. The warning adds a reminder, with the
  limits of option 2.

## How the Pulumi side compares

This comparison does not join the two models (ADR 12, ADR 13).

A Pulumi update gets a lease token when it starts (ADR 20). The Pulumi CLI
sends it with `Authorization: update-token ...` on each checkpoint, event
batch, lease renewal and on `complete`. The store accepts a write only while the update still runs
under that lease. So a write belongs to one update, not to a principal: two
CLIs that share an admin token cannot write into each other's update,
because only the CLI that started the update has its lease token.

The TFE-compatible protocol has a capability of the same kind only for the
upload of one declared version (the upload URL with its random token, ADR 26,
decision 7). It has none for the declare or the unlock. So the OpenTofu side
cannot copy the Pulumi model without a change to the CLI.

## Proposal

Option 4 now, as documentation. Drop option 3, because no supported CLI
sends a lock ID. Take option 2 only if the owner wants a reminder that can
be wrong in both directions.

Reasons:

- The fix that the server can make with today's CLIs is distinct principals.
  The bootstrap tokens give that already, and RFC 20 gives it with fewer
  rights.
- A warning cannot see the risk, so it adds noise more often than safety.
- The decision stays with the project owner.

## Open questions

1. Should the server refuse a declare when the lock is older than a limit and
   the declare comes from a new connection? This needs a definition of a
   client, which the protocol does not give.
2. Should RFC 20 make a CI token that can write state a token that only one
   job uses, for example a token that expires when the job ends?
3. Should OpenDustpan ask the OpenTofu project for a lock ID on state writes?
   This is outside this repository.

## Outcome

Accepted on 2026-10-12 in a changed form ([ADR 44](../adr/0044-workspace-lock-fence-and-overrides.md)).
The lock stays coordination between clients. A force-unlock fences the
old holder with a new lineage. Forced writes and writes without the lock
need an action and a switch that is off by default.
