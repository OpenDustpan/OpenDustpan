---
rfc: 3
title: Domain modules for updates, secrets and OpenTofu state
status: accepted
adr: 20, 23, 26
created: 2026-10-09
was: proposed under number 11
---

# RFC 3. Domain modules for updates, secrets and OpenTofu state

Status: accepted as ADR 20 (part 1, the Pulumi update cycle), ADR 23
(part 2, secrets) and ADR 26 (part 3, OpenTofu state). It was proposed under number 11
until ADR 15 moved every undecided proposal to `docs/rfc/`.

Part 1, the Pulumi update cycle, is accepted as ADR 20.
Part 2, secrets, is accepted as ADR 23.
Part 3, OpenTofu state, is accepted as ADR 26.

## Context

The e2e suite shows what the Pulumi CLI needs after stack management: the
update cycle (create, start, lease, checkpoint, events, complete, cancel),
export and import, and server-side encryption (`preview` and `up` call
`encrypt` before anything else). RFC 1 adds OpenTofu workspaces, locks and
state versions. The rules below were checked against the Pulumi CLI and
OpenTofu sources (`sdk/go/common/apitype`, `pkg/backend/httpstate`,
`internal/cloud`) and the go-tfe client; no code was taken from them.

Following ADR 2, each concern is a deep module: a small set of use cases in
front, invariants and transitions inside, persistence behind narrow ports
documented with their errors.

## Decision

Modules:

- `stack.Updates`, next to `stack.Service`, owns the update cycle. Its ports
  (`UpdateStore`, `CheckpointStore`, `EventStore`) are separate from
  `stack.Store` so existing adapters keep compiling while new ones are built.
- `secret` owns envelope encryption for the Pulumi service secrets provider,
  behind `Wrapper` (key-encryption key) and `KeyStore` (wrapped data keys).
- `workspace` owns OpenTofu workspaces, locks and state versions, behind
  `Store`, `StateStore` and the existing `blob.Store`.
- `internal/token` owns bearer capabilities (lease tokens, upload URLs).
  Tokens are shown once; only SHA-256 digests are stored; `String` redacts.

Update cycle:

- Kinds and statuses use the CLI's wire values. An update mutates unless it is
  of kind `preview` or a dry run: the CLI runs the preview phase of `up`,
  `refresh` and `destroy` with the real kind and `dryRun: true`. Previews
  never block or wait, take no version and are not history. Only one mutating
  update runs per stack; the store enforces it atomically when starting.
- Starting allocates the update's version: one more than any earlier
  mutating update of the stack, so failed and canceled updates keep their
  number in history. The stack's version follows when the update ends.
- A lease lasts 5 minutes and renews for 1 second to 5 minutes. Renewal keeps
  the token so concurrent writes do not fail. An expired lease does not free
  the stack: it stays locked until `pulumi cancel`, because the server cannot
  know whether the engine is still changing resources.
- Every transition is a compare-and-swap on `Update.Revision`, re-applied to
  a fresh read up to three times when it loses (the CLI renews its lease
  while completion is in flight). Completing or canceling again with the same
  final status succeeds without a write: the CLI retries these calls when a
  response is lost.
- Checkpoints are opaque JSON plus schema version and the client's `isInvalid`
  mark; the server never parses them. Without the delta capability, which the
  server does not advertise yet, the CLI sends whole checkpoints through
  `PATCH .../checkpoint` with no sequence number, and the last write wins.
  Sequence-number idempotency applies only to verbatim and delta checkpoints.
  Journaling is not supported yet either.
- Checkpoint and event writes carry the digest of the lease that authorized
  them, and the store accepts them only while the update still runs under it,
  in the same transaction, so a write authorized before a cancel cannot land
  after it.
- Events are opaque JSON, idempotent by sequence number (the CLI counts from 0).
- Import runs as an update of kind `import`, so it shows in history and
  respects the stack lock. Its input is validated before the update exists,
  and the server always ends the update it started, even if the caller hangs
  up.

Secrets:

- One random 256-bit data key per scope, AES-256-GCM, random nonces. Values
  are limited to 1 MiB (ciphertexts may be 29 bytes longer) and batches to
  200 MiB in total, with no count limit: the CLI splits config batches by size
  only. Data
  keys exist only wrapped by the `Wrapper`, which records its ID with each
  key. Ciphertexts and wrapped keys are bound to the scope as associated
  data. Every decryption failure is the same error.
- A scope must be stable for the lifetime of its ciphertexts, so it uses a
  stack ID, not a stack name. Stacks do not have stable IDs yet; adding them
  is a prerequisite for shipping encryption.

OpenTofu state:

- State is opaque; lineage, serial and MD5 come from the client. A new state
  needs the caller to hold the workspace lock, the current lineage and a
  higher serial, unless forced (force never skips the lock). Each lock
  acquisition has its own ID, recorded on the version. Finalizing re-checks
  lock ID, lineage and serial atomically, so a version declared before a
  force-unlock cannot land afterwards, even when the same principal locks
  again. The protocol carries no lock ID, so a stale job sharing a token can
  still declare or unlock under a newer lock; CI jobs need distinct principals.
- Content is uploaded through a capability valid for 10 minutes and checked
  for size and MD5 while streaming. Each upload attempt writes its own blob;
  exactly one attempt finalizes, and an attempt the store rejected deletes
  only its own blob. When finalizing fails with an unknown outcome the blob is
  kept, since the version may point at it; orphans are garbage. A retry of a
  finalized upload with the same capability and content succeeds, because
  go-tfe retries a PUT whose response was lost.

## Consequences

- The update-cycle ports have a conformance suite (`storetest.RunUpdates`)
  and SQLite and PostgreSQL adapters. Those adapters keep only each stack's
  latest checkpoint, with the deployment in the row; per-version history for
  `pulumi stack export --version` and moving deployments to the blob port
  wait for the blob layout decision. `Updates` is not wired into
  `dustpan.NewServer` yet.
- The `secret` and `workspace` ports have no suite or adapter yet; each needs
  its suite first, then the SQL adapters, then wiring.
- HTTP handlers come after adapters, each with its e2e step first.
- Stacks need a stable ID (new column and migration) before `secret` ships.
- A key-encryption key needs a built-in `Wrapper` adapter (a local key from
  the environment) and an operator story for rotation.
- Unwrapping a data key on every request is a KMS call per request once a
  cloud KMS is plugged in; a bounded in-memory cache of unwrapped keys is the
  expected follow-up.

## Outcome

Accepted as [ADR 20](../adr/0020-the-pulumi-update-cycle.md) for part 1 (the
Pulumi update cycle), [ADR 23](../adr/0023-secrets-and-the-built-in-key-encryption-key.md)
for part 2 (secrets) and
[ADR 26](../adr/0026-opentofu-state.md) for part 3 (OpenTofu state).
