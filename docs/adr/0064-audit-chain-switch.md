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
   hash only when the chain was off at some time from the time of the entry
   minus the switch lag of decision 4 to the time of the entry. So an off
   window shorter than the switch lag counts too. The state at a time is the
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
     back along the sequence. A request takes its time before the store
     transaction, and the store gives the sequence in that transaction,
     so two appends to one scope can commit out of time order. So the
     store keeps the times of a scope in order: in the transaction of an
     append, an entry whose time is earlier than the last entry of its
     scope takes the time of that entry.
   - When the installation holds a recorded state and no switch entry
     counts, every scope fails the check.
   - A row with the action `server:set-audit-chain` and a value path that
     names no state breaks the timeline.
   - The timeline must agree with the recorded state. When the record says
     that the log was ever chained or that the chain is on, the timeline
     must hold a switch entry with a hash. Its last switch entry must hold
     the recorded state. Otherwise the timeline is broken.
4. **Every instance follows the recorded state within 2 seconds.** An
   instance reads the recorded state in the transaction of an append and
   keeps it for at most 2 seconds. So a change of the chain reaches every
   instance within 2 seconds, and the check accepts an entry without a hash
   that an instance writes within 2 seconds after a switch to on.
5. **One transaction for a switch.** The switch entries and the recorded
   state commit together in one store transaction, or neither does. In
   that transaction, the store compares the record with the record that
   the instance read before it decided. When another instance changed the
   record first, the store refuses the write. The instance then reads the
   record and decides again, one time. A second refusal fails the start, so
   the server does not serve with a state that it could not record. The
   store takes the time of the switch entries in the transaction, so the
   switch entries never go back in time.
6. **A backup does not carry the state.** The state is bookkeeping of the
   installation, like `schema_migrations`. A restore keeps the state of the
   target installation. The restored log holds the switch entries of the
   backup, so after a restore the server writes one switch entry with a
   hash for the recorded state. Without this entry, a backup with another
   chain state would make every scope fail the check, because the last
   switch entry must hold the recorded state (decision 3).
   - The entry names the person who requested the restore as its subject,
     and holds the change `restore` with the ID of the backup. So an
     auditor can tell it from a switch of an administrator.
   - Before it writes the entry, the server checks the restored log with
     the rules of decision 3, except that the last switch entry may hold
     another state than the recorded one. The entry makes the timeline
     agree again, so only this check can find a backup whose hashes
     someone removed. When the check fails, the server still writes the
     entry, so it can serve. It logs an error and counts the restore in
     `dustpan_audit_restore_check_failures_total`.
   - Just before the swap, the server names the restore in the record
     of the chain. The swap copies the record into the restored data and
     notes there that it replaced the log, in the same transaction. The
     switch entry of the restore removes the name. So a restore that
     stops after its swap gets its entry at the next start, and a restore
     that stops before its swap gets no entry: its log is still the log
     of the installation. A failure of another step after the swap does
     not skip the entry. At a start, the server fences the namespaces and
     writes the entry before it marks the stopped restore as failed, so a
     start that stops between the steps runs them again.
   - A restore trusts the audit log in the backup. Signed audit checkpoints
     (ADR 60 decision 18) are what protect a backup.
   - The check before the entry finds a backup without switch entries or
     hashes only when the target recorded a chained log. A new
     installation records the chain off and no chained log. So a restore
     onto a new installation, the usual target of a disaster recovery,
     cannot find such a backup.
   - After a restore, the server also logs the warning of a start when the
     restored log holds chained entries and the chain is off.
7. **One start entry.** Two instances can start for the first time at the
   same time. Both read no record. The compare of decision 5 refuses the
   write of the second one, which then reads the record of the first one
   and writes no start entry.
8. **One timeline for each check.** The scheduled check builds the
   timeline once and checks every scope against it, in one read
   transaction. A switch of the chain during the check gives no false
   failure.

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
- A person who removes the last switch entry of `@server` makes the
  timeline disagree with the record, so the check finds the cut (decision
  3). A person who removes the entries of `@server` after its last switch
  entry makes the log shorter and leaves a valid chain. The check cannot
  see such a cut. Signed audit checkpoints catch it.
- A person who can compute SHA-256 can write a switch entry with a hash
  that matches its fields. The chain does not stop such a person (ADR 60
  decision 17). Signed audit checkpoints do.
- A person who changes the record of the chain too, next to the switch
  entries and the entries of a scope, can make the timeline agree with the
  changed log. Signed audit checkpoints catch such a change.
- A person who removes the row of the record (`audit_chain_state`) has the
  same effect as a person who changes it: the check then follows the
  switch entries alone. The check does not catch this. Signed audit
  checkpoints catch it.
- A restore trusts the audit log in the backup (decision 6). The check
  before the switch entry of the restore finds a backup without switch
  entries or hashes when the target recorded a chained log, but a backup
  that someone changed with care can pass it. A restore onto a new
  installation, the usual target of a disaster recovery, cannot find a
  backup without switch entries or hashes at all. Signed audit
  checkpoints protect a backup.
- The time of an audit entry can be later than the time of its request,
  by at most the time between two appends to its scope that commit out
  of order.
