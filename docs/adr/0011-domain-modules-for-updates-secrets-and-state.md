# 11. Domain modules for updates, secrets and OpenTofu state

Status: proposed · 2026-10-09

## Context

The e2e suite shows what the Pulumi CLI needs after stack management: the
update cycle (create, start, lease, checkpoint, events, complete, cancel),
export and import, and server-side encryption (`preview` and `up` call
`encrypt` before anything else). ADR 9 adds OpenTofu workspaces, locks and
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

- Kinds and statuses use the CLI's wire values. Previews never block or wait.
  Only one mutating update runs per stack; the store enforces it atomically
  when starting.
- Starting allocates the update's version: one more than any earlier
  mutating update of the stack, so failed and canceled updates keep their
  number in history. The stack's version follows when the update ends.
- A lease lasts 5 minutes and renews for 1 second to 5 minutes. Renewal keeps
  the token so concurrent writes do not fail. An expired lease does not free
  the stack: it stays locked until `pulumi cancel`, because the server cannot
  know whether the engine is still changing resources.
- Every transition is a compare-and-swap on `Update.Revision`.
- Checkpoints are opaque JSON plus schema version; the server never parses
  them. Verbatim checkpoints are idempotent by sequence number and SHA-256.
  Delta checkpoints and journaling are not supported yet: the server will
  not advertise those capabilities, so clients send whole checkpoints.
- Events are opaque JSON, idempotent by sequence number.
- Import runs as an update of kind `import`, so it shows in history and
  respects the stack lock.

Secrets:

- One random 256-bit data key per scope, AES-256-GCM, random nonces. Data
  keys exist only wrapped by the `Wrapper`, which records its ID with each
  key. Ciphertexts and wrapped keys are bound to the scope as associated
  data. Every decryption failure is the same error.
- A scope must be stable for the lifetime of its ciphertexts, so it uses a
  stack ID, not a stack name. Stacks do not have stable IDs yet; adding them
  is a prerequisite for shipping encryption.

OpenTofu state:

- State is opaque; lineage, serial and MD5 come from the client. A new state
  needs the caller to hold the workspace lock, the current lineage and a
  higher serial, unless forced. Finalizing re-checks the serial atomically.
- Content is uploaded through a single-use capability valid for 10 minutes,
  checked for size and MD5 while streaming, and deleted on any mismatch.

## Consequences

- No adapter implements the new ports yet. The next step for each port is a
  conformance suite in `store/storetest` (and the blob layout decision), then
  the SQLite and PostgreSQL adapters, then wiring in `dustpan.NewServer`.
- HTTP handlers come after adapters, each with its e2e step first.
- Stacks need a stable ID (new column and migration) before `secret` ships.
- A key-encryption key needs a built-in `Wrapper` adapter (a local key from
  the environment) and an operator story for rotation.
- Unwrapping a data key on every request is a KMS call per request once a
  cloud KMS is plugged in; a bounded in-memory cache of unwrapped keys is the
  expected follow-up.
