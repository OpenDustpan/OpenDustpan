## Context

Self-service requests are evaluated through gates. Each blueprint rule is a stored gate over typed facts. The server stores each evaluation in a decision record.

## Decisions

1. **Blueprint rules are gates.** When a namespace administrator publishes a blueprint version, each rule of the version is stored as one gate through the gate API. The namespace owns the gate, and the gate links to the blueprint version. A request evaluates the gates of its blueprint version at change.propose. A deny refuses the request with the reasons and the decision record ID. Evaluation at change.planned and change.apply uses the same stored gates under the existing job gate flow and is not changed here.
2. **Delete the old package fully.** Delete the old rule-engine package, its four adapters, its conformance suite, its depguard entry and its docs mentions. Update the layout list and the glossary where either names the old package. Keep the public contract of the gate package.

## Structure

Gate evaluation goes through an unexported type gateEvaluator that only NewGateEvaluator builds. The constructor checks the fact schema, the scope IDs and the engine handle. A mutation test removes the fail-closed branch and the suite must fail.

## Risks

- Self-service code not yet on main leaves part of the removal for a later pull request.
- Stale docs or layout text still names the old package after deletion.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The gate service validates the fact document against the fact schema and rejects unknown or mistyped facts. | Send a fact with a wrong type directly to the service and check that it refuses it. |
| port adapter | The engine denies on an evaluation error, on a timeout and on an empty answer. An empty gate set allows with a record. | Send an allow answer from a fake engine with no gates directly to the evaluator and force an engine error; check the closed deny. |
| HTTP handler | The handler checks the request actions on the unit object and writes the audit entry in the same transaction. | Call the request route without the grant directly and check the 403 answer. |
