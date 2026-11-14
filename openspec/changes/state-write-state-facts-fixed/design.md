## Context

State writes run through the state.write guard before the store transaction commits. The fact builder, the record writer, the approval evaluation and the access check stay in pkg/gate with one constructor-checked config.

## Decisions

1. **Facts from the stored states.** Build state facts from the old and new state carried by the operation, never from caller text and never from a second read. A missing or client-encrypted body gives state.available false with a recorded reason and the write continues under the gates that apply, with no resource count.
2. **Record in the step transaction.** Write the allow record in the same store transaction as the step and name the record ID in the audit entry of the step. Write a refusal or wait record in its own transaction as a record of a refused attempt, never as a claim that a step committed. If the step does not commit, roll back the allow record with it.
3. **Defaults and limits at state.write.** Evaluate approval defaults at state.write with the same combiner as other points, with author, repeated and ineligible signatures refused. Evaluate access limits at the state.write routes through gate.LimitedAuthorizer for unit, namespace, collection and installation objects. Full cover on every other route, the own access-limit record, and the break-glass skip stay in the depended changes.

## Structure

Unexported gate.stateWriteGuard built only by NewStateWriteGuard, which checks the fact schema, the record store and the authorizer are set.

## Risks

- Large state diffs raise fact build time; cap counted resources at 10000 entries and keep raw states out of the record; above the cap state.available stays true for the counted prefix and the record notes the truncation.
- A concurrent write between guard read and commit changes the stored state; the fence check in the same transaction rolls back both the step and the allow record, so the record never describes a state that did not commit.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Fact builder uses only the operation states, maps missing or client-encrypted bodies to state.available false plus reason with no count, caps counted resources at 10000 entries, and keeps raw states out of the record. | Send a client-encrypted state and a state with 20000 resources directly to the fact builder; the first yields state.available false with reason and no count, the second yields a capped count with truncation noted and no panic. |
| service | Service checks workspace lock, manual lock, freeze, override switches and safe delete, records each result, and wraps the step and allow record write in one transaction with rollback on fence loss or store error. | Remove the manual-lock check and show the service test that asserts the fixed control in the record fails. |
| HTTP handler | HTTP handler runs LimitedAuthorizer at each state.write route and maps gate denials to gate.denied with the record ID link. | Call a state.write route with a forbid limit as a directly invoked handler; it returns 403 gate.limited even when grants allow. |
