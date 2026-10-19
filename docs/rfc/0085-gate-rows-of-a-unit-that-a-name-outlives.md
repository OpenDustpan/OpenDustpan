---
rfc: 85
title: Gate rows of a unit that its name outlives
status: draft
created: 2026-10-19
---

# RFC 85. Gate rows of a unit that its name outlives

Status: draft. Open for the project owner.

## Problem

ADR 224 decision 1 keys the configuration of a unit by the scope key
`unit/<namespace>/<tool>/<name>`. The store keeps the rows of a unit
(gates, access limits, rules, path sets) under that key.

A soft delete of a unit frees its name, but it keeps the rows, because a
restore undoes the delete. A team can then create a new unit under the
freed name. The new unit has the same scope key as the deleted unit, so it
matches the gate rows of the deleted unit:

- The new unit meets the gates and the access limits of the deleted unit.
  Nobody wrote them for it.
- An ownership rule of the deleted unit can name approvers. These approvers
  then have rights over a unit they never owned.
- A restore of the deleted unit finds its name in use. The two units share
  one set of rows.

The purge of the deleted unit now keeps the rows when a live unit holds the
name (this stops the loss of the rows of the new unit). It does not stop the
new unit from meeting the old rows before the purge. A rename of a stack
moves its rows to the new name, and refuses a new name that holds the rows of
a deleted unit.

## Options

- **A. Key the rows of a unit by its stable unit ID.** A rename keeps the
  key. A new unit under a freed name starts with no rows. A restore finds
  its own rows. The cost is an edit of ADR 224 decision 1: the scope key of a
  unit holds an ID, which a person cannot read, and the policy IDs and the
  decision records hold the ID. Every other scope key holds a name. A record
  needs a lookup to show the name of the unit.
- **B. Refuse to create a unit whose name holds the rows of a deleted
  unit.** The refusal lasts until the purge or the restore of the deleted
  unit. The key stays as it is. The cost is a new refusal on each create
  path in each tool, and a team that must wait for the retention period to
  end, or purge the deleted unit by hand, before it can reuse the name.
- **C. Move the rows to a tombstone key on a soft delete, and back on a
  restore.** The key of the deleted unit holds the unit ID, so the freed name
  has no rows. A restore moves them back, and refuses when the name is in
  use. The key of a live unit stays as it is. The cost is one move in the
  transaction of each delete and each restore, and a second key shape in the
  store that no API shows.

## Proposal

Product manager recommendation: option C. It keeps the readable key of ADR
224, it needs no new refusal on a create path, and a new unit under a freed
name starts with no rows. Option A is the better design if the project owner
accepts the cost of an ID in the key, because it also removes the copy of
rows in a rename. Option B is the smallest change, but it blocks a team that
only wants to reuse a name.

This RFC is open for the project owner. The recommendation is not a
decision.

## Open questions

- Does a restore of a unit that finds its name in use refuse, or does it
  restore under a new name and move the rows with it?
- Does a tombstone key need a retention limit of its own, or does the purge
  of the unit remove it?
- Does the same problem apply to a collection that a delete frees? A delete
  of a collection removes its rows now, so the expected answer is no.

## Outcome

Empty until the project owner accepts or drops the RFC.
