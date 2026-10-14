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
   switch entry keeps the rule of the first build: an entry without a hash
   matches only before the first chained entry of its scope.
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
