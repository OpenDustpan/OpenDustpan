Issue: #1290.

## Why

**Persona: operator.** An Operator keeps an installation healthy. Staged files from a single upload and content blobs from a refused publish stay on the store today. The Operator needs a background sweep that removes them safely across all namespaces, also next to a restore.

The user path this change must make work:

1. **Server start.** The server starts. A schedule row of the installation starts the registry sweep each interval, with no upload needed.
2. **API.** The Operator reads the sweep counters and the last run time from the metrics endpoint.
3. **Docs page.** The Operator reads the docs page to learn the 24-hour age, the reference rule, the mark expiry and the pause during maintenance mode.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The server is in maintenance mode and the Operator expects the sweep to run | The tool APIs answer 503 with Retry-After. The sweep makes no delete and resumes after maintenance ends. | `opendustpan status` |

Evidence for the claims above:

- Staged files expire after 24 hours only during a later stage in the same namespace, so a one-time upload keeps its files. Proof: Issue #1290: A staged file that no publish moved is deleted after 24 hours, but only when a later stage in the same namespace sweeps the staging directory.
- A refused publish leaves a copied content blob that nothing references. Proof: Issue #1290: A content blob that a refused publish copied is not referenced and is not deleted either (it is content-addressed, so another release may share it).
- The issue asks for a background sweep with a metric and a many-key conformance test. Proof: Issue #1290: Needed: a background sweep over all namespaces (staging by age, content blobs by reference), with a metric, and a conformance test with a blob store that lists many keys.

## What Changes

- A background sweep on the installation deletes staged files older than 24 hours that no publish moved, across all namespaces
- The age of a staged file comes from a creation time that the server stores in SQL, read on the database clock
- The sweep deletes content blobs that no live release references, only when they are older than 24 hours, and keeps shared blobs
- The sweep marks a blob in a SQL table that a restore does not replace, and a mark expires after 1 hour
- The sweep is a server task on a schedule row of the installation and uses no runner pool
- The sweep resumes after a takeover and checks the fencing token before each delete
- The sweep pauses in maintenance mode, so it never deletes a blob that a restore writes
- A metric shows staged deletes, content deletes and the last run time for the installation, as totals over all namespaces
- A conformance test covers a paged listing with many keys

Out of scope:

- Publish, signature and checksum checks of registry releases
- The copy of registry release files in a backup and a restore (its own change)
- Curated upstream rules
- Console pages for the sweep
- A runner pool purpose for sweeps
- A metric label for each namespace

Depends on:

- artifact-registry
- h1-hash-provider-archive-registry
- backups-do-not-copy-files
- `h1-hash-provider-archive-registry` (order of the milestone review)
- `backups-do-not-copy-files` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The Operator sees counters and a last run time. The docs page says how to find why a file remains.
- A fixed 24-hour age can surprise a team that uploads before a weekend publish. The docs page states the age and the mark expiry.

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P14)**, Batch 12: #940 sweep runners: "a pool may have the sweep purpose only when it is an operator (installation) pool, never a namespace pool. Reason: keeps both owner lines, 47.3 "a sweep never runs on user runners" and ADR 343 d3-d5 (owner); fail closed (a namespace pool with the sweep purpose is refused)."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 1 (extended), 3 (current), 5 (extended).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A port for durable operations, with our own adapter. A tool-neutral package in `pkg` defines a port for durable operations: operations with a start key, explicit steps, a claim, retry with backoff, cancel and schedules."
- Quote: "A schedule is a row. A periodic job that needs one runner per installation is a schedule row with `next_run_at`. One conditional `UPDATE` claims a due row and moves `next_run_at` forward."
- Quote: "Control tables stay outside the restore swap."
- Effect: The sweep is a schedule row that starts a durable run with a claim and a fencing token. The mark table and the cursor are control tables, so a restore never brings back an old mark.
- Conflict: none.

**ADR 322: No migrations or compatibility code before the first tag** (owner-tagged: decision 1).

- Decisions that apply: 1 (changed).
- Changed by: ADR 325 changes decision 1; ADR 325 changes decision 5; ADR 325 changes decision 6; ADR 326 extends decision 1; ADR 362 changes decision 5. Chain to the end: ADR 325, ADR 326, ADR 362, ADR 363.
- Current rule: this ADR, with ADR 325, 326, 362.
- Quote: "One baseline for each dialect until the first tag (owner). Each dialect keeps one numbered migration file, the baseline `pkg/store/<dialect>/migrations/<NNNN>_baseline.sql`."
- Effect: New tables edit each baseline in place. No numbered migration is added.
- Conflict: none.

**ADR 286: Self-service, failover and promotion: three renames** (not owner-tagged).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The registry service copies a staged file to its content key in a function `promote`. The glossary entry **Staged file** says that a publish "moves" it. The function takes that word."
- Effect: Text and code say that a publish moves a staged file.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (owner decision conflict): The change runs a background sweep over all namespaces with no statement of which runner pool, if any, executes the sweep. Owner decision P14 says a sweep purpose is allowed only on an installation pool and must be refused on a namespace pool. The spec and tasks name no such check, so the change cannot be built right until it says where the sweep runs. Resolution: answered: The sweep is a server task of the installation and uses no runner pool, so no namespace pool can run it. This follows the owner decision on the sweep purpose and needs no pool check.
- R1.2 (open question): The open question on the staged age (24 hours fixed or an installation setting) is answered as provisional, but the Open questions section and the spec-ready gate still block implementation; the proposal must state which answer the code uses before tasks 2.1 can be built. Resolution: answered: The code uses a fixed 24 hours. The question is closed and the section reads none.
- R1.3 (failure mode): Design decision 1 says the run records last key per page so a takeover resumes without a second delete, but the spec has no scenario for a crash mid-run, a takeover by another instance, or the fencing token check on each delete. The fencing token rule of ADR 72 decision 2 is not applied to the delete step. Resolution: answered: Requirement Resume after takeover and a test cover the fencing token on each delete.
- R1.4 (failure mode): Content delete relies on a SQL reference check in the same run, but a release written concurrently between the check and the blob delete can reference a blob that is then removed. No requirement or scenario serializes the reference check with publish, so a live blob can be lost. Resolution: answered: The mark, delete and re-copy ordering and the scenario Release written during the sweep cover the race.
- R1.5 (missing scenario): A staged file whose publish marker is written after the sweep lists it, or a publish that starts during the sweep, has no scenario. The spec only covers a plain old staged file and a shared blob. Resolution: answered: The scenario Publish during the sweep covers it.
- R1.6 (missing scenario): Maintenance mode and the 503 answer named in the proposal error table have no requirement: the spec does not say whether the sweep pauses during maintenance or restore. Resolution: answered: The requirement Maintenance pause covers it.
- R1.7 (wrong claim): The proposal's error table names the command `opendustpan installation status`, and the code check reports that this command does not exist. The one command that helps is unsupported by the input. Resolution: answered: The error now uses `opendustpan status` only.
- R1.8 (scope): The ADR check says a sweep schedule row uses ADR 72 decision 3, but the proposal's usability section and tasks do not name the namespace-pool or installation-pool identity of the schedule, leaving the sweep's owning scope unclear. Resolution: answered: The design names the installation scope: one schedule row of the installation, in the worker component.
- R1.9 (failure mode): A hostile or very large key listing is bounded per page, but no requirement covers a store that returns an error or a stalled page mid-listing, nor a retry rule with backoff for the durable run. Resolution: answered: The requirement Store error retry and a test cover it.
- R1.10 (open question): The metric names (registry_sweep staged deleted, content deleted, last run) are not tied to the required per-namespace counters that the user path asks for; the spec gives installation-wide counters only. Resolution: answered: The metric is installation-wide totals over all namespaces, with no namespace label. A per-namespace counter is out of scope, and the user path no longer asks for one.
- R2.1 (wrong claim): The design and the spec say the sweep marks a blob and a publish reads the marks, but the proposal never says where the mark and the publish marker are stored. The ADR check does not cover the new tables and does not say whether they are restore-swapped or control tables (ADR 72 decision 5). A mark in a backup list could return after a restore and block a live publish. Resolution: answered: The mark table and the cursor are control tables outside the restore swap (ADR 72 decision 5), stated in the design and the ADR check.
- R2.2 (failure mode): A sweep mark is written and the sweep then crashes or stays in maintenance mode, so the mark stays. Every later publish of that blob must copy it again, and nothing says when a mark expires or is cleared. The spec has no scenario for a stale mark or for a mark whose delete failed. Resolution: answered: A mark expires after 1 hour. The requirement Mark expiry and its two scenarios cover a stale mark and a failed delete.
- R2.3 (failure mode): The sweep deletes a blob in the blob store and the reference check is in SQL, so the two are not one transaction. A publish that copies the blob again after the mark but before the store delete can lose the new copy to the delete. The design does not say how the delete and the re-copy are ordered. Resolution: answered: The design decision on mark, delete and re-copy ordering uses a row lock and a re-check in the delete transaction.
- R2.4 (missing scenario): The scenario 'Publish during the sweep' only covers a publish that moves a file after the listing. The design adds a publish marker that the sweep re-reads before each staged delete. No scenario tests the marker, and the spec has no requirement for it. Resolution: answered: The moved flag on the staged row replaces the publish marker, and the scenario Publish during the sweep tests it.
- R2.5 (scope): The 'Installation pool only' requirement needs a runner pool object with a purpose field that can be set. Nothing in the input shows that this field or an API to set it exists. The change does not say whether it adds the purpose, or whether the sweep uses a runner pool at all, since the sweep runs as a durable run on a server instance. No task or route covers the refusal, its actions or its audit entry. Resolution: answered: The sweep is a server task and uses no runner pool. The pool purpose requirement and its error are removed, and the pool purpose is out of scope.
- R2.6 (wrong claim): The proposal error table says the Operator reads the sweep state in maintenance mode and gets a 503. No route that reads sweep state is defined, and the user path reads metrics only. The task 3.1 mentions 'route actions of the status read' with no spec requirement. The error row has no matching requirement or scenario. Resolution: answered: The false error row is removed. The only route is the metrics read, and the error now says only that the sweep pauses in maintenance mode.
- R2.7 (missing scenario): The 24-hour age is measured on some clock, but the spec does not say which. ADR 72 uses the database clock. The staged file age may come from the blob store modification time or from a database row. A skewed clock between instances can delete a young file early. Resolution: answered: Age reads a stored creation time on the database clock, stated in the requirement text.
- R2.8 (failure mode): The per-namespace metric counters use namespace IDs as labels. The change does not bound the label count for an installation with many namespaces, and it does not say what happens to counters of a removed namespace. Resolution: answered: The metric has no namespace label, so the label count is bounded.
- R2.9 (missing scenario): The spec does not cover a blob or key under the registry prefix that no release, no mark and no staged row knows, such as one written by an interrupted copy. It is not said whether the sweep deletes such a key at once or applies an age to it. A fresh copy that a publish has not yet referenced could be deleted by mistake. Resolution: answered: The scenario Fresh unknown key kept and the 24-hour age rule for unknown keys cover it.
- R2.10 (scope): The task list has a conformance test for a paged listing, but no task adds the many-key conformance test that the issue asks for. The task 1.1 mentions a paged listing only as part of a staged-age test, and the spec has no requirement for a store with many keys. Resolution: answered: The requirement Paged listing and the task for the many-key conformance test cover it.
- M.11 (dependency): The sweep deletes blobs that a restore of backups-do-not-copy-files writes. It also relies on a mark table that a restore does not replace. Resolution: answered: backups-do-not-copy-files is now in depends_on. The scenario Restored blob kept and the test TestRegistrySweepKeepsRestoredBlob cover a blob that a restore wrote. The sweep pauses in maintenance mode and keeps each blob that a release references.

## Open questions

none

Questions considered and answered:

- How does the sweep run across namespaces without a later upload? ADR 72 decisions 1 and 3: a schedule row with next_run_at and a claimed durable run. A missed tick runs once.
- Where does the sweep run, and does it need a runner pool? Owner decision, 13 Nov 2026 (queue P14): a sweep purpose is allowed only on an installation pool, never on a namespace pool. This change uses no runner pool: the sweep is a server task of the installation, in the worker component. No namespace pool can run it.
- Is the staged age 24 hours fixed or a setting? The code uses a fixed 24 hours, as the issue states. No setting is added.
- Does the sweep need a schema migration? ADR 322 decision 1: edit the baseline in place. No numbered migration.
- Do the mark tables survive a restore? ADR 72 decision 5: they are control tables outside the restore swap, so an old mark never returns.
- What if a restore writes registry blobs while the sweep runs? A restore runs in maintenance mode, and the sweep makes no delete in that mode. After the restore, the restored release rows reference the blobs, so the reference rule keeps them. The change backups-do-not-copy-files owns the copy itself.
- Which clock measures age? The database clock (ADR 72 decision 2). The age reads a stored creation time, never the blob store time.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
