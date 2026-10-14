# 64. The audit chain switch, its timeline and its check

Status: accepted · 2026-10-14

Accepted by the project lead under the owner's delegation of 2026-10-13. It
needs no owner sign-off, because it does not weaken a default: the chain
stays opt-in, as ADR 60 decision 17 says.

## Context

ADR 60 decision 17 makes the hash chain of the audit log opt-in. Decision
19 keeps the chain on for an installation that already holds chained
entries, and RFC 22 records how an administrator turns it off. ADR 60 does
not say how the check tells an entry that the server wrote with the chain
off from an entry whose hash someone removed. It also does not say how
several instances of the server agree on the state of the chain, or how a
backup and a restore treat that state.

A review of the first build found three gaps:

- An entry whose hash someone removed passed the check in two cases: in a
  scope whose chain was never off, and after the chain went off and on
  again.
- Each instance read the state only at its start, so two instances could
  write with different states for a long time.
- The change entry and the saved state were two writes, so a crash between
  them could leave one without the other.

## Decision

1. **Switch entries are the timeline.** Each change of the chain writes a
   switch entry in the scope `@server`: the kind `change`, the action
   `server:set-audit-chain`, and the value path `/audit/chain/on` or
   `/audit/chain/off`. A switch to off holds a hash, so a chain that goes off
   ends with a chained entry.
2. **The first start writes the start state.** When the installation has no
   recorded state, the server writes one switch entry for the state before
   the start. The entry is on, with a hash, when the log holds chained
   entries. It is off, without a hash, when the log holds none. So the
   switch entries describe the whole history of the chain.
3. **The check follows the timeline.** The check accepts an entry without a
   hash only when the chain was off at the time of the entry, or at that
   time minus the switch lag of decision 4. The state at a time is the
   state of the latest switch entry at or before that time. Before the
   first switch entry, the state is the state of that entry. A store with no
   switch entry and no recorded state keeps the rule of the first build: an
   entry without a hash matches only before the first chained entry of its
   scope.
   - The check builds the timeline from a walk of `@server` from sequence
     1 up to its last switch entry. A switch entry counts only when the
     walk accepts it: its hash matches its fields and links it to the
     entry before it. The one exception is a switch entry to off without a
     hash at sequence 1, because a new installation writes it so.
   - When the walk of `@server` fails before its last switch entry, when a
     switch entry after sequence 1 has no hash, or when a switch entry is
     earlier than the switch entry before it, the timeline is broken and
     every scope fails the check.
   - A scope with a row below sequence 1 fails the check, because the walk
     starts at sequence 1.
   - After a chained entry of a scope, an entry without a hash also needs a
     switch to off after that chained entry, less the switch lag, or a time
     within the switch lag after a switch to on.
   - Next to an entry without a hash, the time of the entries must not go
     back along the sequence.
   - When the installation holds a recorded state and no switch entry
     counts, every scope fails the check.
4. **Every instance follows the recorded state within 2 seconds.** An
   instance reads the recorded state in the transaction of an append and
   keeps it for at most 2 seconds. So a change of the chain reaches every
   instance within 2 seconds, and the check accepts an entry without a hash
   that an instance writes within 2 seconds after a switch to on.
5. **One transaction for a switch.** The switch entries and the recorded
   state commit together in one store transaction, or neither does.
6. **A backup does not carry the state.** The state is bookkeeping of the
   installation, like `schema_migrations`. A restore keeps the state of the
   target installation and writes no switch entry. After a restore, the
   server logs the warning of a start when the restored log holds chained
   entries and the chain is off.

## Consequences

- Each new installation writes one switch entry at its first start, also
  with the chain off.
- An administrator who turns the chain off or on sees the change in the
  audit log of `@server`, and every instance follows it within 2 seconds.
- An entry without a hash that an instance writes more than 2 seconds after
  a switch to on fails the check. A clock on one instance that runs more
  than 2 seconds behind the clock of another instance can make the check
  fail.
  Run the instances with synchronized clocks.
- The check cannot prove that an entry without a hash is unchanged.
  Signed audit checkpoints (ADR 60 decision 18) are the anchor outside the
  database.
- The check cannot prove a scope unchanged from inside the database when
  the scope holds no chained entry after a switch to off. A person who removes the
  hashes of the entries after the last chained entry of a scope can move
  their times into an off window that started after that chained entry,
  and the check still answers valid. Signed audit checkpoints cover this
  case.
- A person who removes the tail of `@server`, for example a last switch
  to on, makes the log shorter and leaves a valid chain. The check cannot
  see such a cut. Signed audit checkpoints catch it.
- A person who can compute SHA-256 can write a switch entry with a hash
  that matches its fields. The chain does not stop such a person (ADR 60
  decision 17). Signed audit checkpoints do.
- Two instances that start for the first time at the same time can both
  write a start switch entry. The second one has no hash and does not
  count, and it holds the same state as the first, so it does no harm.
