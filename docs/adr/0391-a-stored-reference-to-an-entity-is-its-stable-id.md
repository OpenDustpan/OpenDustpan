# 391. A stored reference to a unit or a collection is its stable ID

Status: accepted · 2026-11-20. Decided by the project owner (owner, 2026-11-20). The owner chose ADR 362 decision 1. The name match of ADR 273 decision 3 ends

Changes:

- Changes ADR 273 decision 3: a stored reference to a consumer unit or collection of another namespace is its stable ID, never its name. A name match is not a stored reference.

## Context

Review of the first pull request of output sharing found a conflict. ADR 273 decision 3 lets a sharing rule name a unit or a collection of another namespace as its consumer. It also says:

> So the store keeps the reference of such a consumer, and matches it at each read. A rename in the other namespace ends the match. A new unit or a new collection with the old name matches.

The review found that the store keeps such a consumer by its lowercased name. That follows ADR 273 decision 3, and it conflicts with ADR 362 decision 1 (owner, 2026-11-08):

```text
"Every stored reference to an entity uses the stable ID of the entity
when a user can rename the entity, or delete and recreate it. A name is
a label for people only."
```

A user can rename a unit and a collection. A user can also delete one and make it again. So a new unit with an old name gets the read access that a rule gave to the old unit. ADR 362 decision 1 prevents this failure for groups.

The project manager asked the owner which rule wins. The owner decided on 20 November 2026:

> Stable ID always. No entities should be keyed by name.

OpenDustpan has no users, and no tag shipped a rule. So the change needs no data conversion.

## Decision

1. **A stored reference is the stable ID (owner, 2026-11-20).** A sharing rule can name a unit or a collection of another namespace as its consumer. The rule stores the stable ID of that unit or collection. It stores no name for a match. A rename changes no rule. A unit or collection that someone deletes and makes again has a new ID. It does not match the old rule. This changes the bullet of ADR 273 decision 3 that begins "So the store keeps the reference of such a consumer". It changes nothing else in that decision.
2. **No entity has a name key (owner, 2026-11-20).** Each stored reference to an entity uses the stable ID of the entity. This holds when a user can rename the entity. It also holds when a user can delete the entity and make it again. A name is a label for people. The store may keep a name next to the ID for display. A name never decides a match, a grant or a rule. This is ADR 362 decision 1. This ADR does not change ADR 362.
3. **The other words of ADR 273 decision 3 stay.** The producer decides alone. The rule needs no acceptance by the other namespace. The write of the rule carries the fence of the producer namespace only. A denial of a read by a job of another namespace answers 404. Its audit entry goes to the audit log of the producer namespace only.

## Consequences

These points follow from the decisions. They are not words of the owner.

- The docs page of output sharing no longer says that a new unit with the old name matches. It says that a rename does not end a rule. It says that a unit that someone deletes and makes again does not match.
- The save of a rule takes the stable ID of the consumer. The owner did not decide how a person in the producer namespace learns the ID of a unit or collection of another namespace. This also touches a property of ADR 273 decision 3: the save does not tell whether the consumer exists (ADR 220 decision 11). See "Not decided here".
- The first code pull request of output sharing changes its rule store to the stable ID.

## Not decided here

- How the save of a rule learns the stable ID of a consumer in another namespace. Also open: whether the save checks that the ID exists. A save that stores the ID without a check keeps that property. It needs the ID from the person who saves. The project manager takes this question to the owner if the code lane finds no other reading.
- The check of other stored references by name in accepted ADRs, RFCs and OpenSpec changes. ADR 362 lists some under "Follow-ups for decision 1". The audit in the pull request of this ADR lists more. Each needs its own ADR or RFC.
