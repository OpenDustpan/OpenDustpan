# 44. A force-unlock fences the old holder, and overrides are opt-in

Status: accepted · 2026-10-12. Decisions 4 and 6 extended by ADR 239

Changed by:

- ADR 239 extends decisions 4 and 6: a client-encrypted lock release needs the
  action and the switch.
- ADR 335 extends decision 5: a state version with no lock meets the gates before it is written.

## Context

[RFC 63](../rfc/0063-workspace-lock-holder.md) found that an OpenTofu
workspace lock belongs to a subject, and that the TFE-compatible protocol
sends no lock ID on a state write. Two clients with one token look like one
holder.

The project owner and the project lead discussed the problem on 2026-10-12
and came to these conclusions:

- The lock coordinates clients that cooperate. It cannot protect the
  infrastructure: a client that skips the lock calls the cloud APIs before
  the server sees any write.
- The lineage and serial checks (ADR 26, decision 3) protect the state.
  No write that read an old state replaces a newer one without force.
- The one real hazard is a stale job after `tofu force-unlock`: a job that
  stopped responding wakes up and writes under the lock of the next holder.
- `tofu state push -force` and `tofu apply -lock=false` are explicit
  overrides by a principal that can write. The server does not try to
  protect a user from them, but it lets an administrator turn them off. The
  state history keeps every version, so a user can restore one.

A first proposal raised the serial at force-unlock. A check of the OpenTofu
source at v1.11.14, v1.12.7 and v1.13.1 showed that it does not work. The
CLI adds one to its own serial before each upload and does not undo it when
the upload fails (`internal/states/remote/state.go:212`,
`internal/cloud/state.go:195`). A stale job whose first upload the fence
refuses sends serial n+2 on its next upload, and the server accepts it. Every
rule that looks only at the serial fails in the same way.

The CLI sets the lineage of its state only when the lineage is empty
(`internal/states/remote/state.go:223`, `internal/cloud/state.go:207`).
After it takes the lock, it reads the state again
(`internal/backend/local/backend_local.go:60` and `:73`). A new lineage
therefore fences the old holder and not the new one.

## Decision

1. **A force-unlock fences with a new lineage.** In the transaction that
   releases the lock, `force-unlock` writes a new state version. Its content
   is the current state with a new lineage and serial 1 more than the
   current serial. The history marks the version as a fence and names the
   principal that forced the unlock. A workspace with no state gets no
   fence.
2. **The old holder fails.** Every later upload of the stale job carries the
   old lineage, so the server refuses it with the lineage error of ADR 26
   (422). The CLI reports "Failed to persist state to backend" and writes
   `errored.tfstate`. A saved plan from before the fence fails its lineage
   check too (`internal/backend/local/backend_local.go:383`).
3. **The new holder continues.** It reads the fenced version after it takes
   the lock, so it writes with the new lineage.
4. **A forced write is a capability.** A state write with `force` set needs
   the action `unit:write-forced` on the unit. The bootstrap policy gives it
   only to administrators. Fine-grained roles (RFC 19) can grant it later.
5. **A write without the lock stays refused.** Today a declare on an
   unlocked workspace answers 409 (`workspace.heldBy`). That stays the
   default. A principal with the action `unit:write-unlocked` may write to
   an unlocked workspace.
6. **A switch turns each override on.** A namespace setting and a unit
   setting allow forced writes, and two more allow writes without the lock.
   All are off by default. A unit setting can only narrow the setting of
   its namespace. Each setting has a REST route with its own action (ADR 17).
   An override needs both the action and the switch.
7. **Every override is in the audit log.** An allowed forced or unlocked
   write writes an audit entry with the action, the unit and the serial.
8. **The error names the way out.** A refused override names the setting
   and the action that would allow it.
9. **Out of scope.** The server makes no attempt to tell two clients with
   one token apart while one of them holds the lock. Distinct principals for each job
   (RFC 20) remain the advice of ADR 25.

## Consequences

- Lineage now means "the same state since the last force-unlock", not
  "the same state since creation". The history shows each change.
- A user who pushes a backup from before a fence needs a forced write, so
  needs the action and the switch.
- Forced writes become an administrator action. Before this ADR, any
  principal that could write state could force a write.
- The e2e suite gets a step for each OpenTofu version in its matrix: take the
  lock, stop a job before its upload, force-unlock, take the lock in a second
  job, then check that the stale job fails and the second job succeeds. It
  also gets steps for a refused forced write and a refused unlocked write,
  and for both allowed by action and switch.
- The Pulumi CLI has its own overrides (for example `pulumi stack import
  --force`). The same actions and switches can cover them. That needs its
  own change, because the update cycle of ADR 20 uses leases, not locks.
