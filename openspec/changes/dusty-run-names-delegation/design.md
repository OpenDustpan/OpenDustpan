## Context

Dusty acts for one person. A run records the person and the credential that started it, and the loop resolves both before each step and each tool call. RFC 121 decision 9 adds the delegation: the person consents once to a scope and a mode, and each run names that consent. This change puts the delegation into the run, into the resolve of each step, into the check of each tool call and into the audit entry. It also adds the parts a run needs to exist in a binary: the stored run, the routes and the wiring.

## Decisions

1. **The run names one delegation.** `agent.Run` and `agent.NewRun` get a `DelegationID`. The start refuses an empty ID (`agent.delegation-required`) and refuses a delegation that is unknown, ended, of another person, for another agent, or not covering the namespace of the run (`agent.delegation-invalid`). The ID never changes for a run.
2. **A start never comes from a delegated token.** The start route accepts a console session or a personal access token only (RFC 121 decision 9). A delegated token gets `agent.delegated-token-refused`.
3. **One resolve for the credential and the delegation.** The `Credentials` port keeps its job. A new small port `Delegations` resolves a delegation ID to its person, agent, mode, expanded scope and end. The loop calls it at the start of each step and before each tool call, next to `Credentials.Resolve`. The run keeps no copy of the answer between steps. A delegation that ended returns `ErrDelegationEnded`, and the run ends with the NEW stop reason `delegation-ended`.
4. **Both parts allow a tool call.** The `Toolset` allows a call only when the authorization of the person allows it and the expanded scope of the delegation lists the action on the object. The mode only selects the actions that the scope lists (RFC 121 decision 12). A refusal writes the entry of a denied call with the cause `delegation-scope`.
5. **The audit block names the delegation.** The `agent/1` block already has a delegation ID. The loop sets it from the run for each call, with the credential kind `agent-run` (RFC 121 decision 44). The subject of the entry stays the person.
6. **Stored runs.** One SQL adapter for each dialect implements `agent.RunStore`, and runs the conformance suite in `internal/agent/agenttest`. The table gets the column `delegation_id`. The schema stays one file for each dialect (no tag has shipped).
7. **Routes.** The routes of the table of ADR 120 start, read, list and cancel a run. Each takes the same `agent:use` check as the tools. The start takes an idempotency key.
8. **Wiring.** The server registers `agent.TurnKind` in its durable runner next to the reap kind, with the real `Credentials` and `Delegations`.

## Structure

`Delegations` is a port in `internal/agent`, small, and implemented by the delegation service of `person-delegates-dusty`. `Run.DelegationID` is set only by `RunStore.Create`. A run read has no setter. The loop gets the resolved delegation as a value in `TurnContext`, so a handler cannot keep it between steps.

## Contract

- `POST` start: body with `delegationId`, the prompt, the namespace and the optional limits. Answer: the run with `delegationId`, the person and the state.
- `GET` run and list: both return `delegationId` and the stop reason. A list filter `delegationId` selects the runs of one delegation.
- Stop reason `delegation-ended`: added to the list of `StopReason` values next to `credential-ended`.
- Error codes (NEW): `agent.delegation-required`, `agent.delegation-invalid`, `agent.delegated-token-refused`.

## Risks

- A delegation that ends during a model call lets that one model answer arrive. The run records it and ends before the tools step, so no tool runs.
- A person who has no delegation cannot start a run, also on a one-person installation. The error names the list route. A later change may add a default. This change adds none (no hidden rule).

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Port adapter | `RunStore.Create` refuses an empty delegation ID and stores the ID with the run in the same transaction | `agenttest` case: create with an empty ID fails; the stored row keeps the ID |
| Service | The start resolves the delegation: person, agent, end, namespace cover | table: unknown, ended, other person, other agent, other namespace |
| Loop | Each step and each tool call resolve the delegation again | a revoke between steps ends the run with `delegation-ended` and no tool runs |
| Toolset | A call needs the scope of the delegation and the grants of the person | a call outside the scope fails with the grants of the person intact |
| HTTP handler | The start route refuses a delegated token and a break-glass session | a delegated token gets the 403 and writes no run |
