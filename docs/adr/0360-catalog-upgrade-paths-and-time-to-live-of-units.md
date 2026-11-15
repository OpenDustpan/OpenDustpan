# 360. Catalog: three paths to a new blueprint version, and time to live of units

Status: accepted · 2026-11-07. Records decisions 14.1 and 43.1 of the project owner of 18 October 2026

Changes:

- Extends ADR 112: a request upgrades by three paths, and a time to live is a unit setting. This answers two open questions of ADR 112 in part.
- Extends ADR 308 decision 2: an upgrade does not move a request between git and the store.

## Context

The project owner made these decisions in the owner decision file 2026-10-17, entries dated 18 October. A check on 7 November 2026 found no ADR that states them.

ADR 112 lists two open questions. The first: who migrates the answers of existing requests when a new version renames or deletes an input, or adds a required one? The second: does a blueprint get a time to live for its units? Version 1 of ADR 112 asks the requester to submit an upgrade with valid answers. ADR 308 records other decisions of the same day for the Catalog.

This ADR writes the words of the owner. It adds no decision of its own. It tags each decision with "(owner, 2026-10-18)". A detail that the words do not settle is under "Not decided here".

## Decision

1. **Three paths to a new blueprint version (owner, 2026-10-18).** A Catalog request moves to a new blueprint version by one of three paths:
   1. **Requester upgrade.** The server maps the answers by name. The requester fills the new required inputs.
   2. **Migrations and batch upgrades.** The blueprint declares migrations, written in Expr. The platform team can run batch upgrades.
   3. **Publisher edits of answers.** The publisher edits the answers.
2. **Time to live is a unit setting (owner, 2026-10-18).** The time to live is a setting of a unit. A blueprint gives a default for it. A namespace gives a maximum for it.

## Consequences

- An upgrade is a new revision of the request with a higher version. Its answers must pass the checks of the new version (ADR 112 decision 2). This holds for each of the three paths.
- An upgrade keeps the home of the request. It never moves a request between git and the store (ADR 308 decision 2).
- The Expr in a migration is Expr inside a blueprint. The limits of ADR 112 decisions 6, 11 and 12 apply to it.
- A time to live is a setting of a unit, so the API shows its effective value. It also shows the source of the value: the unit, the blueprint default or the namespace maximum. A default that the API does not show is a hidden rule.
- A blueprint that gives a default time to live changes the document of the blueprint. ADR 112 decision 13 gives the rules for its schema version.
- ADR 112 and ADR 308 get a pointer to this ADR.
- Each route of these decisions lands with its e2e step first. A feature that a user can see gets its page in the user documentation in the same pull request.

## Not decided here

- The form of the migrations section in the blueprint document: its fields, what a migration can read, and what it can write.
- The form of a batch upgrade, for example whether it is a record of its own, and which approval rules apply to it.
- The audit entries of a publisher edit of answers, and the action that it needs.
- What happens to an upgrade when a migration fails to run, or when a required input has no answer.
- The forms of a time to live, for example a date or a time with no activity, and how the server measures inactivity.
- What the server does at the expiry of a time to live, and the notices before it.
- Whether the maximum of the namespace counts from the creation of a unit. Whether it holds for administrators. Whether a collection can set one.
- What the server does with a time to live above a maximum that is already set. What happens to a unit when the maximum becomes lower.
- The words for the time to live in the API and in the user documentation. `GLOSSARY.md` lists "TTL" under _Avoid_ for the retention period of a deleted unit. A new term in `GLOSSARY.md` waits for the decision on these words.
- The upgrade of providers and modules.
