## Context

Pulumi jobs run through the job service and the runner. CLI writes enter through the Pulumi Cloud-compatible adapter. Both paths call the same gate service with the facts of the moment.

## Decisions

1. **Job points in the job service.** Add the three job evaluations in the job service, not in the runner: change.propose at job start, change.planned after the preview stores the plan, change.apply before dispatch of the update phase.
2. **CLI point in the Pulumi adapter.** Add the state.write check in the Pulumi update create handler, before the store create, with operation kind update for a Pulumi update from a person CLI. A deny returns the Pulumi shape with code and message.
3. **No double evaluation for runner updates.** Run state.write only for an update that a person creates through the Pulumi Cloud-compatible API. A runner update of a Pulumi job meets change.propose, change.planned and change.apply and skips a second state.write, so one runner update is never evaluated twice.
4. **Reuse of the OpenTofu slice.** Reuse the gate service and the fact producers of the depended slice. Add only a Pulumi adapter call site and Pulumi fact values. Duplicate no store logic from the OpenTofu path.

## Structure

Unexported evaluator type with a constructor that loads the compiled policy set, the fact schema version and the scope chain. Only the constructor makes a value, and it refuses an unknown decision point.

## Risks

- Pulumi CLI version drift in the refusal shape
- Double evaluation of change.apply and state.write for one runner update

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP adapter | Handler checks facts, scope chain and caller action before it calls the engine. | Send a forged fact document and a cross-unit gate directly to the handler; the handler rejects and evaluates only its own unit facts. |
| gate service | Service evaluates all gates of the point in one request, applies deny over requirement over warn over allow, and fails closed on engine error. | Force an engine diagnostic error and a conflicting allow; the service stops the change with rule failed, never allow. |
| store port | Store holds the gate rows keyed by stable unit ID and the decision record with gate versions; a rename of the stack changes no key. | Rename the stack and replay an old record; the new evaluation resolves the same unit ID and cites the current gate version. |
