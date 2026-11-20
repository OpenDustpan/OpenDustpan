## Context

Dusty drafts under a live delegation. The person confirms the draft in `person-confirms`. The change order package, the draft state and the action names are not on main and not decided (see the open questions of the proposal).

## Decisions

1. **The server resolves the delegation at the draft.** The draft write calls `Resolve` of `person-delegates-dusty` in the same transaction as the write (RFC 121 decisions 9 and 31). An ended delegation refuses the write with `delegation.ended`.
2. **The proposer is the person.** The draft stores the stable ID of the person, of the agent and of the delegation (ADR 362 decision 1, RFC 121 decision 26).
3. **One effect for one key.** A repeat of the draft write with the same run, turn and call index returns the first draft (ADR 120 decision 11).
4. **No approval.** The draft records no approval (ADR 299 decision 35).

## Structure

The draft is built only by a constructor that needs the person, the agent, the delegation and the key. A draft with no delegation cannot be built.

## Contract

The draft record, its state and its routes are open questions 1 and 2. This design adds no endpoint and no schema until the owner answers them.

## Risks

- The agent drafts outside the scope of the delegation. The scope check runs at the draft and again at the confirm.
- The draft is read as an act of the person. The draft names the agent and the delegation.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | The service resolves the delegation, checks the scope for the unit and refuses an ended delegation. | Draft after the end, outside the scope and with a forged delegation ID. Each is refused and stores nothing. |
| port adapter | The adapter stores the draft with the person, the agent and the delegation, and enforces one row for each key. | Write two drafts with one key and check that one row exists. |
| HTTP handler | The handler requires authentication and checks the action at the point of use for the unit. | Send a draft with a token that lacks the action and check the refusal. |
