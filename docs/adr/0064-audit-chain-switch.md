# 64. The audit chain is a start setting, and a change is a migration

Status: accepted · 2026-10-15

## Context

ADR 41 decision 13 gives every audit entry the hash of its own fields and of
the previous entry of its scope. ADR 60 decision 17 made that chain opt-in
and off by default, and decision 19 kept it on for an installation that
already held chained entries. A first design switched the chain on and off
while the server ran. It needed switch entries as a timeline, a lag of 2
seconds for instances that had not seen a switch yet, a compare of the
record for instances that switched at the same time, and a read of the
recorded state in each append. Reviews kept finding races and false alarms
in that design.

The project owner decided on 2026-10-15:

> The flow I'm imagining right now is the chain requires a flag at startup,
> like the audit log itself. Changing the setting requires a server reboot.
> If the setting differs from what was set before, the server starts up in
> maintenance mode and requires a button click or an API/CLI call to change
> the setting, which starts a migration. Until the migration is done, the
> server is left in maintenance mode.

The owner also decided that the chain is on by default, and that turning it
off needs a person with both the operator and the administration families
and a fresh MFA step.

## Decision

1. **The chain is on by default.** `--audit-chain` (`DUSTPAN_AUDIT_CHAIN`,
   or the settings file) takes `on` or `off`. The default is `on`.
2. **It is a start setting.** The server reads it once at start. No API, no
   console control and no reload of the settings file (ADR 63) changes it
   while the server runs, as for `--audit` (ADR 68 decision 3).
3. **The database keeps the state of the chain.** The table
   `audit_chain_state` holds the state (`on` or `off`), when it last
   changed, who changed it, whether a change waits, and whether the swap
   of a restore replaced the log. The table is bookkeeping of the
   installation, like `schema_migrations`: a backup does not carry it. An
   installation without a row counts as `on`. Before this ADR the chain was
   always on, so an upgrade needs no migration.
4. **A start compares the setting with the state.** When they agree, the
   server starts normally. Each append reads the stored state in its own
   transaction. An append computes a hash when the state is on and the last
   entry of its scope is not an end entry. Otherwise it writes no hash. A
   missing row counts as on.
5. **A different setting starts the server in maintenance mode.** The
   server notes in `audit_chain_state` that a change waits, enters the
   maintenance mode of ADR 38, and changes nothing yet.
   - The note is the reason `audit-chain` of the maintenance mode. The
     table `installation` that holds the maintenance state is in the backup
     format, so the reason stays out of it. While the note holds, the
     server refuses a leave of maintenance mode and a restore with 409,
     and refuses an approval while a restore runs. Each one leaves only its
     own maintenance mode.
   - An instance whose setting differs from the stored state answers 503 on
     most routes. The probes, GET of the metrics route, and the files of
     the console stay available. GET and POST of the audit chain route stay
     available. The maintenance routes stay available. The maintenance
     routes pass so an operator can clear stale active work on an instance
     whose setting differs. This holds after another instance approved the
     change, until the instance restarts with the stored state.
   - While a change waits or migrates, every instance answers 503 on every
     route except the same routes. Reads of the audit API wait too.
   - Each instance caches the stored state for the gate with the same
     cache limit as the installation state. The cache decides admission
     only. Each append reads the stored state in its own transaction.
6. **A person approves the change.** `GET /api/server/audit/chain` answers
   the stored state, the setting of the instance, whether a change waits,
   and whether its migration runs. It needs `server:read-audit`.
   `POST /api/server/audit/chain` approves the change to the setting of the
   instance that receives it, and answers 202. The setting of the instance
   decides the actions of the route. The console button and the commands
   `opendustpan audit-chain status` and `opendustpan audit-chain approve`
   call the same routes. An approval while no change waits answers 409.
   - Turning the chain on needs the action `server:set-audit-chain`
     (operator family).
   - Turning the chain off needs `server:set-audit-chain` (operator family)
     and `server:disable-audit-chain` (administration family), so "both
     families" is literal, and a fresh MFA step. Until built-in MFA exists, the MFA step
     asks for nothing (ADR 56 decision 5). A test proves that the step is
     required as soon as MFA asks for one.
   - The approval writes an audit entry in `@server` with the action
     `server:set-audit-chain`, and starts the migration.
   - The approval claims the migration in one conditional write on
     `audit_chain_state`. Only one approval on all instances wins. Another
     approval answers 409. A claim expires after 15 minutes, so a migration that
     stops without a release blocks a new approval for that time at most.
7. **The migration.** The server stays in maintenance mode until the
   migration ends. Before it writes, it waits two cache limits after the
   enter, so every instance refuses writes. The migration writes in each
   scope in turn:
   - **On:** a start entry, with the action `server:start-audit-chain`. The
     start entry holds a hash with an empty previous hash. Its changes hold
     `digest`, the digest of every earlier entry of its scope (decision 8),
     and `entries`, their count. Earlier entries keep their rows: audit
     entries are append-only, and copies of them already sit in sinks.
   - **Off:** an end entry, with the action `server:end-audit-chain`, that
     names the person who approved the change. It holds a hash linked to
     the entry before it, so the chain ends with a chained entry.

   To off, the server writes the end entries then stores off with no
   wait. To on, the server stores on with a wait first, then writes the
   start entries, then stores on with no wait. The early store makes new
   appends hash at once. The digest of a start entry covers an append
   before it. Then the server waits two cache limits and leaves
   maintenance mode. A migration that stops before it ends keeps a
   waiting change, so the next start asks for approval again. A second
   start entry in a scope does no harm: its digest covers the first one.
   A scope gets no start entry only when it is empty or its last entry is
   already a start entry, so a migration that runs twice writes one.
8. **The digest.** The digest of the entries of a scope before a start
   entry is a running SHA-256, in order of sequence. It starts as the empty
   string. For each entry, the next digest is the hex SHA-256 of the
   previous digest, the entry hash, the stored previous hash and the stored
   hash, joined with the unit separator (0x1F). The entry hash is the hash
   of the stored fields of the entry that ADR 41 decision 13 defines, with
   the optional fields of ADR 60 decision 5 and decision 10 of this ADR,
   and with an empty previous hash. So the digest covers every field and the stored hash
   columns. A verifier can compute it again from the old rows to prove that
   they did not change after the chain started.
9. **The check.** For each scope, the chain starts at its last start entry,
   or at sequence 1 when the scope holds none. From there, each entry holds
   a hash that matches its fields and links to the entry before it, with no
   gap, up to now or up to an end entry. After an end entry, entries hold no
   hash.
   - A start entry also checks its digest against the earlier entries. It
     also requires each earlier entry that holds a hash to match its own
     fields and stored previous hash.
   - With the chain on, the scope must not end after an end entry.
   - With the chain off, a scope without a chain is valid only when its
     first entry comes after the time the chain went off: the scope did not
     exist while the chain was on.
10. **The hash takes no key, and covers the fence.** The entry hash is a
    plain SHA-256 over the documented encoding, with no key or secret of
    the installation, so another installation can check a namespace chain
    that it receives. Each entry of a namespace scope holds the failover
    version of the namespace when the store appended it (`fence_version`),
    read in the transaction of the append. The hash covers it as an
    optional field, `fence_version=<n>`, after `changes`, when it is above
    0. Entries in `@server`, and entries from before this field, hold 0 and
    keep their hashes. So a verifier sees where a forced failover forked
    the chain of a namespace. A later change may store entries of another
    installation as they are, with the same sequence and hashes: nothing in
    this ADR depends on the installation that wrote an entry.
11. **A restore keeps the state of the target.** After the swap, the server
    compares the restored log with the stored state. The log agrees when,
    with the chain on, each scope ends with a chained entry that is not an
    end entry, and with the chain off, each scope ends with an entry
    without a hash or with an end entry. When it does not agree, the
    installation stays in maintenance mode with the reason `audit-chain`,
    and the approval of decision 6 runs the same migration on the
    restored log. The swap notes in the same transaction that the log was
    replaced, so a restore that stops after the swap gets the comparison at
    the next start.

## Consequences

- A new installation has a chain from its first entry. An operator who
  turns the chain off restarts the server with `--audit-chain=off`, and a
  person with both families approves the change.
- An operator who changes the setting restarts every instance. Until each
  instance runs with the stored state, it serves nothing but the probes,
  the metrics and the routes of decision 6.
- The check of a start entry reads every earlier entry of its scope, so a
  check costs one read of the log, as before.
- The check cannot prove that an entry before a start entry, or after an
  end entry, is unchanged from inside the database alone. The digest proves
  it to a verifier who kept the digest outside the database, for example in
  a sink or a signed checkpoint (ADR 60 decision 18).
- A person who can compute SHA-256 and write to the database can write a
  new chain. Signed checkpoints catch such a change.
- A person who deletes the row of the state, or changes it, can make the
  check follow a state that the installation never had. Signed checkpoints
  catch such a change.
- A restore trusts the audit log in the backup. Signed checkpoints protect
  a backup.
- Retention (ADR 68 decision 2) deletes old entries. A later ADR decides
  where the chain of a scope starts after such a deletion.

## What this ADR replaces

ADR 1 forbids edits to an accepted ADR. So this ADR records what it
replaces:

- **ADR 60, decision 17:** the chain is off by default, a change of the
  switch writes an entry of the kind `change` in `@server`, and the check of
  a scope starts at the first entry after the chain was last turned on.
  Decisions 1, 6, 7 and 9 replace this.
- **ADR 60, decision 19:** an installation that holds chained entries keeps
  the chain on. Decisions 1 and 3 replace this: every installation is on
  until a person approves a change.
