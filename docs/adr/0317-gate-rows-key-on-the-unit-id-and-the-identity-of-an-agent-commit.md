# 317. Gate rows key on the unit ID, and the identity of an agent commit

Status: accepted · 2026-11-01. Records decisions of the project owner of 19 and 20 October 2026 that are later than the ADRs they change

Changed by:

- ADR 319 changes decision 2: the trailer `Co-Authored-By` of an agent commit.

Changes:

- Changes ADR 224 decision 1: the key of the gate rows of a unit.
- Changes ADR 76 decisions 7 and 8: the identity of an agent commit and the trailer.

## Context

The project owner made these decisions in the owner decision files of 19 and 20 October 2026. ADR 224 and ADR 76 are older. Decision 1 is later than ADR 224 decision 1, so it wins. Decision 2 changes two decisions of ADR 76. ADR 76 decision 7 is a PM addition, so the owner line wins by rank. ADR 76 decision 8 is a decision of the owner, and the owner line is later, so it wins by date.

ADR 224 decision 1 builds the scope key of a unit from the tool and the name of the unit. ADR 76 decision 7 says that the `Co-Authored-By` trailer is the disclosure.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **Gate rows key on the unit ID (owner, 2026-10-19).** The owner said: "gate rows key on the stable unit ID". The owner said also: "a new unit with the same name starts clean".
2. **The identity of an agent commit (owner, 2026-10-20).** The owner said: "agent commit identity becomes `Claude <noreply@anthropic.com>` (author and DCO sign-off) in all new briefs". The owner said also: "Still no model name, no Co-Authored-By trailer". The owner said also: "Pushed history unchanged".

## Consequences

- ADR 76 and ADR 224 get a pointer to this ADR.
- (PM, for the owner): decision 1 needs a conformance test for a unit that a name outlives.
