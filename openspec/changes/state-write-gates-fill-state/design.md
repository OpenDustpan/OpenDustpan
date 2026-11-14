## Context

Only the restore of a state version fills the state facts today. The unlocked write on the OpenTofu HTTP backend also holds the full new state in its body before the gate runs.

## Decisions

1. **Fill the unlocked write only.** Build the existing gate.StateChange from the real current and new state, count instances by address, recheck the current state version in the write transaction, and fail closed with a catalog code and an audit entry. Provisional until the owner decides RFC 119. Build after gate-subject-job-token-has for the unlocked-declare check and use the strict envelope check of state-facts-follow-ups-one as the definition of a fake envelope.
2. **Save-time visibility only.** Refuse a new state.write gate that reads state.resources or state.removed without state.available, with gate.invalid. Stored gates keep zero-value evaluation.

## Structure

The existing type gate.StateChange in pkg/gate/statechange.go is the one carrier of the facts. No new tool and no migration.

## Risks

- The read of the body before the write is a new read path and needs its limits.
- A gate that reads state.available first must not be refused.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| limit | 10 | MB | body read of one unlocked write |
| limit | 20000 | instances | one fact build |
