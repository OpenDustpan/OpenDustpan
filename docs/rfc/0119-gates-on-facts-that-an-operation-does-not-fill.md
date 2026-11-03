---
rfc: 119
title: Gates that read a fact which an operation does not fill
status: discussing
created: 2026-11-03
---

# RFC 119. Gates that read a fact which an operation does not fill

Status: discussing. An open question for the project owner. Until the owner
decides, `main` keeps the evaluation of ADR 224 and ADR 226 unchanged.

## Problem

Issue #2260 found that the state facts of `state.write` were never filled. A
gate such as `context.state.removed.contains("aws_s3_bucket.data")` never
matched, and nothing showed that.

The change that closes the issue fills `state.resources` and `state.removed`
for the restore of a state version (operation kind `rollback` on the
TFE-compatible API). This is the only write where the server holds both the
current state and the new state (ADR 224, "Facts with no plan"). For each
other operation, `state.available` is `false`, `state.resources` is `0` and
`state.removed` is the empty list.

So the silent fallback still exists for those operations. A gate with the
condition `context.state.removed.contains("x")` is valid at `state.write`.
For a lock, a force-unlock, a delete or an unlocked write it reads an empty
list, and it never matches. The person who writes the gate sees no sign of
this.

ADR 226 decision 4 says: a condition that reads a field of a group with the
flag `available` must also read the flag. Nothing checks that rule when the
person saves the gate, and nothing checks it when the engine evaluates the
gate.

## What `main` does now

- The server fills the state facts for the restore of a state version, and
  records them in the decision record.
- The server gives `state.available` `false` for each other operation.
- A restore of a state version that the client encrypted is one more case
  where `state.*` is not available. The restore goes on, and the decision
  record warns with the reason "state is client-encrypted". The server does not
  add the reason to the fact document, because a new fact field is a new
  schema version (ADR 226).
- The guide `docs/guides/state-write-gates.md` lists, for each operation, which
  facts the server fills.
- A gate evaluates a fact that is not available as the zero value, as ADR 226
  says. This RFC does not change that.

## Options

### A. Do nothing more

The guide shows the matrix. A gate author reads it.

- Cost: none.
- Gives: no change to the evaluation. The silent fallback stays for a person
  who does not read the guide.

### B. Runtime: a gate that reads an unavailable fact is a gate in error

At evaluation, the server finds the groups that the condition reads (the gate
already stores these in `Reads`). If a group has the flag `available` and the
flag is `false` for the unit, the gate is in error for that unit. The gate
then follows its `onError` setting: `fail` refuses the write with
`gate.rule-failed`, and `skip` skips the gate. The decision record names the
group.

- Cost: a change of the meaning of ADR 224 and ADR 226. A gate that reads a
  group with the flag, and that today evaluates as the zero value, would fail
  after the change. This includes the groups `plan`, `cost`, `budget`, `drift`,
  `resourcePolicy`, `quota`, `dependents` and `paths`, not only `state`.
- Gives: no gate passes by accident on a fact that the server did not read.
  The decision answers "why did this fail" with the group and the reason.
- Risk: a gate that checks the flag first (`available && removed > 0`) must
  not fail. The server must tell a read of the flag from a read of another
  field, so the `Reads` data needs a per-field form.

### C. Config time: the save of a gate refuses an unfilled fact

At the save of a gate at `state.write`, the server refuses a condition that
reads `state.resources` or `state.removed` with `gate.invalid`, and names the
fact, unless the condition also reads `state.available`.

- Cost: the check is partial. A gate is saved for a scope, and the point
  `state.write` covers several operations. The save cannot know the operation
  of a later write, so the check can only require the flag read. It cannot
  refuse a gate for a lock.
- Gives: a gate author sees at once that the fact is not always filled. No
  change at evaluation time.

### D. B and C together

C teaches the author at the save. B protects the gate that was saved before,
and the gate that reads the flag wrongly.

## Proposal

Option D, in two steps. First C, because it changes no evaluation and no
stored gate. Then B, as a new ADR that changes ADR 226 decision 4, after the
owner accepts the change of meaning for the other groups with the flag.

## Open questions

1. Is a gate in error (B) the right result for a fact that is not available,
   or must the gate evaluate as `false`?
2. Does B hold for every group with the flag, or for `state` only?
3. Do existing gates change at upgrade? If B refuses a stored gate that
   passed before, the project needs an upgrade check (ADR 38).
4. Must the matrix of operations and facts be data that the API shows
   (a capabilities document), so that the console shows it when the author
   writes a gate?

## Checklist

- [x] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9). The change adds no write path and no cross-namespace read.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
