---
version: 2
---

Issue: #1290.

## Why

**Persona: operator.** An Operator keeps an installation healthy. Staged files from a single upload and content blobs from a refused publish stay on the store today. The Operator needs a background sweep that removes them safely across all namespaces, also next to a restore.

The user path this change must make work:

1. **Server start.** The server starts. A schedule row of the installation starts the registry sweep each interval, with no upload needed.
2. **API.** The Operator reads the sweep counters and the last run time from the metrics endpoint (NEW: registry sweep series).
3. **Docs page.** The Operator reads the docs page to learn the 24-hour age, the reference rule, the mark expiry and the pause during maintenance mode.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The server is in maintenance mode and the Operator expects the sweep to run | The tool APIs answer 503 with Retry-After. The sweep makes no delete and resumes after maintenance ends. | `opendustpan status` |

Evidence for the claims above:

- Staged files expire after 24 hours only during a later stage in the same namespace, so a one-time upload keeps its files. Proof: Issue #1290: A staged file that no publish moved is deleted after 24 hours, but only when a later stage in the same namespace sweeps the staging directory.
- A refused publish leaves a copied content blob that nothing references. Proof: Issue #1290: A content blob that a refused publish copied is not referenced and is not deleted either (it is content-addressed, so another release may share it).
- The issue asks for a background sweep with a metric and a many-key conformance test. Proof: Issue #1290: Needed: a background sweep over all namespaces (staging by age, content blobs by reference), with a metric, and a conformance test with a blob store that lists many keys.
- The registry package and its upload route exist on main. Proof: Context pack main list: package pkg/registry and route POST /api/dustpan/v1/namespaces/{}/registry/uploads.

## What Changes

- A background sweep on the installation deletes staged files older than 24 hours that no publish moved, across all namespaces
- The baseline of each dialect gets a creation time column (Unix milliseconds, set on the database clock) and a moved flag on the staged row; a staged key with no row is an unknown key
- The age of a staged file comes from that stored creation time
- The sweep deletes content blobs that no live release references, only when they are older than 24 hours, and keeps shared blobs
- The reference check and the publish insert of a reference both take a row lock on one content-key row in the same SQL transaction, so the two are ordered
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

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The registry package exists and holds the registry service. (`pkg/registry (package)`)
- The registry upload route exists. (`POST /api/dustpan/v1/namespaces/{}/registry/uploads (route)`)
- The registry error code registry.not-staged exists. (`internal/errcode/catalog_registry.go:23`)
- A sweep of unreferenced blobs exists for stack updates and is a model for the test. (`pkg/stack/updates_test.go:1628 (func TestSweepDeletesUnreferencedBlobs)`)
- Components start background work by component. (`pkg/dustpan/components_internal_test.go:71 (func TestStartBackgroundByComponent)`)
- An installation sweep route exists for deleted units. (`ANY /api/server/deleted-units/sweep (route)`)

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
- Effect: New tables and columns edit each baseline in place. No numbered migration is added. Later ADRs in the chain (325, 362) change decisions 1, 5 and 6; this change touches only the in-place edit, which they keep until the first tag.
- Conflict: none.

**ADR 286: Self-service, failover and promotion: three renames** (not owner-tagged).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The registry service copies a staged file to its content key in a function `promote`. The glossary entry **Staged file** says that a publish "moves" it. The function takes that word."
- Effect: Text and code say that a publish moves a staged file.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: main_facts[0]: "where" is not file:line (func Name); a code check fails: main_facts[1]: "where" is not file:line (func Name); a code check fails: main_facts[5]: "where" is not file:line (func Name).

Questions considered and answered:

- How does the sweep run across namespaces without a later upload? ADR 72 decisions 1 and 3: a schedule row with next_run_at and a claimed durable run. A missed tick runs once.
- Where does the sweep run, and does it need a runner pool? Owner decision, 13 Nov 2026 (queue P14): a sweep purpose is allowed only on an installation pool, never on a namespace pool. This change uses no runner pool: the sweep is a server task of the installation, in the worker component.
- Is the staged age 24 hours fixed or a setting? Fixed 24 hours in code, as the issue states. No setting is added.
- Does the sweep need a schema migration? ADR 322 decision 1: edit the baseline in place. No numbered migration.
- Do the mark tables survive a restore? ADR 72 decision 5: they are control tables outside the restore swap, so an old mark never returns.
- What about staged files that have no row or no creation time? No tag shipped the staged row shape, so no conversion code is written (ADR 322 decision 2). A staged key with no row is an unknown key and follows the 24-hour listing-age rule.
- Which clock measures age? The database clock (ADR 72 decision 2) for stored times. For an unknown key only, the listing time of the store.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
