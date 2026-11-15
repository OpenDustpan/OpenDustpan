## Context

Dusty drafts under a live delegation. The person confirms the draft into a change order in state proposed. The later confirm of the proposed order belongs to person-confirms.

## Decisions

1. **Reuse the draft store with a distinct confirm-draft step.** Dusty calls changeorder.Service with an actor that holds the person, the agent and the delegation. CheckAgent allows only draft writes. Only the confirm-draft creates the order. The confirm-draft uses its own NEW route and NEW action changeorder:confirm-draft, distinct from the confirm route and action of person-confirms.

## Structure

The unexported type delegationPropose in internal/agent has the constructor checkLiveDelegation. Only that constructor builds an actor with an agent reference. It checks scope, unit and expiry.

## Risks

- The agent proposes outside the delegation scope
- The agent act is recorded as a person act
