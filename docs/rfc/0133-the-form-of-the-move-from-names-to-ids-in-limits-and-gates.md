---
rfc: 133
title: The form of the move from names to IDs in limits and gates
status: draft
created: 2026-11-19
---

# RFC 133. The form of the move from names to IDs in limits and gates

Status: draft. It changes no decision. [ADR 362](../adr/0362-owner-decisions-of-8-november-2026-stable-ids-store-order-mysql-baseline-hashes-and-portable-backups.md)
decision 1 (owner, 2026-11-08) already says that a Cedar access policy names a
group by its stable ID. ADR 362 "Not decided here" says that a new ADR or an
RFC must decide "the form of the migration from names to IDs in each code path
listed under Consequences, because it can change a stored shape". This RFC is
that question for limits and gates.

## Problem

Access limits and gates name a group, and exemptions name a subject, by name.
Evaluation matches the name. A group that someone deletes and makes again with
the same name inherits the limit, and also an `unless` exemption. This is the
case that ADR 362 decision 1 forbids. Two review findings (an access limit that
names a group, and an exemption by subject) found the same question.

What the code touches: the request of the Cedar adapter, the group direction of
a limit, the list of group IDs that the store gives, the check at save time,
the docs and the example in ADR 223 decision 11.

## Options

1. **ID only, refuse the name form at save.** A limit or gate holds the stable
   ID of a group or a principal. The save check refuses a name. The API shows
   the name next to the ID as a label. No alias, no conversion code. Reason: no
   tag shipped the name form, so no stored shape needs a path (ADR 72, and the
   rule of no migration code before the first tag). Cost: an operator who wrote
   a limit by name writes it again by ID. Gain: one rule, the smallest code.
2. **ID with a name alias that the API accepts.** The save check turns a name
   into the ID of the group that has that name at that moment, and stores the
   ID. Cost: a silent rule at save time (a name means "the group now"); the
   answer to "which group did I mean" depends on the time. This is a hidden
   rule. Gain: easier to write by hand.
3. **Keep names, add a version check.** The limit stores the name and the ID
   that the group had when the limit was saved. Evaluation refuses a limit
   whose ID does not match. Cost: two fields for one reference, and a stale
   limit fails closed until someone saves it again. Gain: old text keeps
   working.

## Proposal

Option 1. It follows ADR 362 decision 1 without a conversion path, and it
leaves no silent rule (CLAUDE.md "No magic"). A refused name gives an error
that names the one command that helps (list the groups with their IDs).

## Hostile test, for any option

Delete a group and make it again with the same name. The old limit must not
apply to the new group. An `unless` exemption must not apply to it either.

## Open questions

- Does the owner accept option 1 for limits, gates and exemptions together?
- Do saved limits of a dev installation need a reset note in the docs, or does
  `dev reset` cover it (ADR 324)?
- The follow-up list of ADR 362 "Consequences": is each other code path that
  stores a name decided by this RFC, or does each need its own?
