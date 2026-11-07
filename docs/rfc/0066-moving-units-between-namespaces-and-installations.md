---
rfc: 66
title: Tooling to move units between namespaces and installations
status: draft
created: 2026-10-12
---

# RFC 66. Tooling to move units between namespaces and installations

Status: draft. ADR 244 answers part of it for an import into OpenDustpan:
the scope, the form (a REST API with a CLI) and the check of one unit, then
of a whole namespace. ADR 244 decision 16 reports source principals and never maps them. The project owner asked for this RFC on 2026-10-12, in the
answers to RFC 17. ADR 47 decided that a unit never moves between
namespaces, and that export and import are the way out. This RFC asks what
help a person needs for that way, and when. It decides nothing.

## Problem

ADR 47 gives a unit no move action. A person who wants a unit in another
namespace, or on another installation, exports it and imports it again. The
owner gave two reasons for this rule in the answers to RFC 17: a unit is
bound to its namespace, and an automated move raises too many questions. The
owner asked: "What happens to their access grants in the target namespace?"
and "What happens to their bound profiles in the target namespace?"

Today the way out of OpenDustpan is the standard commands of each CLI
(`docs/guides/migrate-out.md`). They move the latest state of a unit and do not
move the history. The way in is the import (ADR 29). Nothing tells a person
before the move which parts of a unit the export and the import cannot carry
over, and which parts break.

The owner wrote: "I think a migration guide and at a later stage CLI tooling
might be a better fit here, with checks for things that will inevitably break
import/export." The owner said that the scope of this tooling is not clear
yet. In this repository "migration" means a SQL schema migration, so this RFC
says "move", "export" and "import" (GLOSSARY.md).

## First ideas

- A guide first. It lists what a person must check and redo by hand when they
  move a unit to another namespace or installation.
- Later, a command with a check mode that changes nothing. It reads a unit and
  reports what the export and the import cannot carry over, before anyone
  moves the unit.
- The check covers at least the items below. Each item is a candidate and
  needs a look at the code.

| Item | Why it may break |
| --- | --- |
| Access grants | A grant names a principal and a unit reference in the source namespace (ADR 13). The target namespace has other principals and other grants. |
| Bound profiles | A profile of the source namespace may bind to the unit (RFC 5). The target namespace may have no such profile. |
| Secrets | `secret.Scope` includes the namespace (ADR 19). The ciphertext of the source cannot be read in the target without a re-encryption (ADR 23). |
| References between units | A unit may read the output of another unit (RFC 15). The other unit stays in the source. |
| Labels and groups | A label or group name may not exist in the target (RFC 4). |
| History | The standard CLI commands move the latest state only. |
| Schedules, triggers, policies | These are not part of the state (RFC 42 reports them as "not moved"). |

## Options

- **Do nothing beyond ADR 47.** Each person finds the breaks by trial. This
  costs no code. It risks a half-moved unit with wrong access.
- **A guide only.** One page per direction. It costs little and cannot check a
  real unit.
- **A guide, then a CLI with a check mode.** The owner's direction. It costs a
  command and a set of checks to keep current with each new feature that
  binds to a unit.
- **A move action in the server.** ADR 47 rejects this.

## Open questions

- What is the scope of the tooling? The owner says it is not clear yet.
- Is the check a command of the `opendustpan` binary (RFC 7), a separate tool,
  or a REST API (ADR 17) that a client calls?
- Who may run a check, and which action does it need? A check reads access
  grants, so it may need more rights than the move itself (ADR 17).
- Does the check also run on a whole namespace, to move it to another
  installation, or only on one unit? ADR 4 moves a namespace for failover with
  its contents.
- Does the tooling map a source principal to a target principal, or only
  report that the grant is lost?
- How does each new feature that binds to a unit add its check, so that the
  list does not go out of date?

## Owner direction (2026-11-07)

The owner decided part of this RFC on 2026-11-07
([ADR 346](../adr/0346-a-unit-moves-between-collections-never-between-namespaces.md)):

- No one-click move of a unit between namespaces, because it would break
  grants. A person exports and imports, with the move check of ADR 244.
- A person can move a unit between collections of one namespace. The server
  lists what breaks, and the person confirms the list.

A move between installations stays open.
[RFC 126](0126-placement-replication-and-moves.md) part C proposes the rest:

- The list of effects of a move between collections: one provider for each
  domain that stores a collection reference, with a test that fails when a
  provider is missing, and a confirm digest so that a person never confirms
  a list that is no longer true.
- A unit between installations: export and import, as between namespaces.
- A namespace between installations: with federation, a replica, then a
  planned failover, then a detach with the revocation of the old copy's
  keys; without federation, an offline move from the namespace backup
  (ADR 218) with a "restore as a move" mode. The source keeps a moved
  marker, so the old URL answers 421 with the target.
- New carry-over findings of the move check of a whole namespace for the
  references to the installation scope that do not move.

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).
