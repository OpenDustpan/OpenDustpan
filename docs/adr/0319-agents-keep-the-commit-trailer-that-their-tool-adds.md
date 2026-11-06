# 319. Agents keep the commit trailer that their tool adds

Status: accepted · 2026-11-01. Decided by the project owner on 1 November 2026

Changes:

- Changes ADR 317 decision 2: the trailer `Co-Authored-By` of an agent commit.

## Context

ADR 317 decision 2 records an owner decision of 20 October 2026. One part says: "no Co-Authored-By trailer". The owner made a later decision on 1 November 2026, 22:00 UTC. It replaces that part.

## Decision

1. **The trailer is not a rule (owner, 2026-11-01).** The verbatim quote of the owner:

   ```text
   I don't care what commit trailer agents add or don't add. Just do whatever causes the least friction please.
   ```

2. **Agents keep the trailer of their tool (PM, for the owner).** An agent does not remove the trailer that its tool adds. An agent does not add a trailer that its tool does not add.

## Consequences

- ADR 317 gets a pointer to this ADR.
- The rest of ADR 317 decision 2 stays as it is.
