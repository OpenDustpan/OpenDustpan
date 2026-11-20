## Context

Confirmation stays an act of a person. The change order package, the confirm route and the draft state are not on main and not decided (see the open questions of the proposal). This design holds only the points that ADR 344, ADR 354, ADR 385 and RFC 121 settle.

## Decisions

1. **Only a person confirms.** A request of an agent is refused and written as a denial entry (ADR 344 decision 2, ADR 299 decision 35).
2. **The proposer is the person.** The draft stores the stable ID of the person. Four-eyes counts the person as the author, and a later end of the delegation does not change it (RFC 121 decision 26, ADR 362 decision 1).
3. **A fresh step follows the setting.** The handler, the service and the store read one effective value of `freshAuthEnabled` (ADR 385 decision 2). An access token never gives the step.
4. **The audit entry comes first.** The confirmation entry is written in the same transaction as the confirm, so no apply entry can come before it.

## Structure

The confirm check is built only by a constructor that needs the author, the confirmer and the approval rule. No other code path builds a valid check.

## Contract

The route, the action and the states are open question 1. This design adds no endpoint and no schema until the owner answers.

## Risks

- The delegator counted as a different person breaks four-eyes.
- The apply starts before a person confirms.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The service refuses an agent confirmer, counts the delegator as the author and checks the fresh step when the setting is on. | An agent confirms with a delegation and with its own token. The delegator confirms and then approves under `authorMayApprove` false. Each is refused. |
| port adapter | The adapter writes the confirm and its audit entry in one transaction and keeps the author fixed. | End the delegation after the draft and check that the author does not change. |
| HTTP handler | The handler requires a console session when a fresh step is needed, and checks the action for the object. | Confirm with an access token while `freshAuthEnabled` is on and check `mfa.fresh-step-required`. |
