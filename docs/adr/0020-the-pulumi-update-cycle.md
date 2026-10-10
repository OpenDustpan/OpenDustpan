# 20. The Pulumi update cycle

Status: accepted · 2026-10-10

## Context

The Pulumi CLI needs an update cycle after stack management: create, start,
lease, checkpoint, events, complete and cancel, plus export and import. RFC 3
proposed the rules for it, and part 1 of RFC 3 covers only this cycle. The
domain model exists (`stack.Updates`, with SQLite and PostgreSQL adapters and a
conformance suite). It is not wired into `dustpan.NewServer`, and the Pulumi
API has no update route.

The rules came from reading the Pulumi CLI sources. Nine points stayed open
after that reading: who may cancel, what a preview does, how `pulumi cancel`
finds the update, and others
([issue 32](https://github.com/OpenDustpan/OpenDustpan/issues/32)).

ADR 19 gives each stack a stable ID (`stk-...`) that stays the same after a
rename. The tables for updates, checkpoints and events reference that ID.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the agent's reviewed recommendation to become the decision.
The owner can supersede it with a new ADR.

## Decision

The "Update cycle" section of [RFC 3](../rfc/0003-domain-modules-for-updates-secrets-and-state.md),
and its `stack.Updates` and `internal/token` module items, this ADR accepts as
written. Part 2 (secrets) and part 3 (OpenTofu state) of RFC 3 stay open.

The key rules of that section:

- **One mutating update per stack.** The store enforces it atomically when an
  update starts. An update mutates unless its kind is `preview` or it is a
  dry run.
- **Version allocation.** Starting an update allocates its version: one more
  than any earlier mutating update of the stack. Failed and canceled updates
  keep their number. The stack version follows when the update ends.
- **Lease.** A lease lasts 5 minutes. A renewal lasts from 1 second to 5
  minutes and keeps the same token.
- **Compare-and-swap.** Each transition is a compare-and-swap on
  `Update.Revision`. When it loses, the service re-applies it to a fresh read,
  up to 3 times.
- **Idempotent end.** Completing or canceling again with the same final status
  succeeds without a write.
- **Lease-digest check.** Checkpoint and event writes carry the digest of the
  lease that authorized them. The store accepts them only while the update
  still runs under that lease, in the same transaction.
- **Import is an update.** It has kind `import`. It shows in history and
  respects the stack lock. The server always ends the update that it started.
- **Tokens.** `internal/token` shows a token once and stores only its SHA-256
  digest.

Answers to the open points:

1. **An expired lease does not free the stack.** An expired lease blocks
   writes, but the update stays `running` and the stack stays locked. Only
   `pulumi cancel` frees the stack. RFC 18 decides a force-release action.
2. **Anyone with the right to update the stack may cancel.** The right is a
   tool-neutral action on the unit (ADR 13, ADR 17). The CLI sends no lease on
   cancel, so the cancel route does not need one.
3. **Previews take no lock and no version, and are not history.** A preview is
   an update of kind `preview` or with `dryRun: true`. It still gets a lease
   and events. It never gets a checkpoint.
4. **The stack response shows the running update.** The Pulumi CLI finds the
   update this way. `pulumi cancel` calls `CancelCurrentUpdate`
   (`pkg/backend/httpstate/backend.go`). That function reads the stack with
   `GetStack` and takes the update ID from `apitype.Stack.ActiveUpdate`
   (`sdk/go/common/apitype/core.go`). The JSON name is `activeUpdate`, a string
   without `omitempty`. If it is empty, the CLI stops with an error. Otherwise
   it posts to the cancel route of that update. The update kind is not used
   there. So the stack response always contains `activeUpdate`: the ID of the
   running mutating update, or `""` when none runs. A cancel route that takes an
   update ID is enough. A stack-level cancel route is not needed.
5. **Milestone 1 stores whole checkpoints.** They arrive through `PATCH`, and
   the last write wins. The server advertises no delta capability and no
   journaling. The model keeps idempotency by sequence number for verbatim and
   delta checkpoints later.
6. **The server never rolls back.** A failed or canceled update leaves the
   latest checkpoint that the engine saved. The engine records its own pending
   operations in it. The ticket
   [Decide where checkpoints live](https://github.com/OpenDustpan/OpenDustpan/issues/35)
   decides their storage and their history.
7. **`outputs` and `secretsProviders` are not stored in milestone 1.** The CLI
   sends them on complete. The server stores them only if an e2e step shows
   that the CLI needs them.
8. **No reaper.** Nothing changes the state of an expired lease. The API may
   show a lease as expired. It derives this on read, with no state change.

Wire values keep their own spelling, for example the status `cancelled`.

## Consequences

- Every endpoint lands with its e2e step first (CLAUDE.md). The Pulumi e2e
  scenario has `config set --secret`, `stack export`, `preview` and `up`. It
  has no `refresh`, `destroy`, `cancel` or `stack import`. Those steps come
  first, with a scenario for a crashed CLI that `pulumi cancel` recovers.
- Open work, from the [brief for issue 32](https://github.com/OpenDustpan/OpenDustpan/issues/32#issuecomment-6103244335):
  1. HTTP routes for create (for each kind), start, lease renewal, checkpoint,
     events, complete, cancel, export, import, and get and history of
     updates. Also the capability list, and the authorization action of each
     route (ADR 17).
  2. Wire `Updates` into `dustpan.NewServer`. Run the same suite on
     PostgreSQL.
  3. The e2e steps from the first item above.
  4. The `activeUpdate` field in the stack response (decision 4).
  5. The model has no place for `outputs` and `secretsProviders`. Decision 7
     leaves it that way unless an e2e step shows a need.
  6. The behavior of `stack export --version` and of history for each version
     depends on the checkpoint storage decision.
- A crashed CI job locks its stack until a person runs `pulumi cancel`. This
  is the cost of never losing state. RFC 18 may add a force-release action.
- The stack key of updates, checkpoints and events is the stable stack ID from
  ADR 19. A rename moves no rows.
- A later ADR can add delta checkpoints and journaling. The model already
  has the sequence number ready for them.
- A console can show a stuck lock, because the API derives "expired" on read.
