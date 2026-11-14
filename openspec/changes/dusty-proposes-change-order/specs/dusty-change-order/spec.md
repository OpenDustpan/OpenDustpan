## ADDED Requirements

### Requirement: dusty-proposed-order-after-confirm-draft

When the delegating person confirms the draft of an agent under a live delegation, the server SHALL create the change order in state proposed with the person as proposer and the agent and the delegation named.

#### Scenario: Draft then confirm-draft
- **WHEN** Dusty writes a draft and the delegating person confirms the draft
- **THEN** The change order is in state proposed and names the person, the agent and the delegation.

### Requirement: dusty-draft-scope

If the delegation is expired, revoked or does not cover the unit, then the server SHALL deny the draft.

#### Scenario: Expired delegation
- **WHEN** Dusty drafts after the delegation expired
- **THEN** The server denies the draft and stores nothing.

### Requirement: dusty-draft-once

When the agent repeats a draft write with the same run, turn and call index key, the server SHALL return the first draft without storing a second draft.

#### Scenario: Repeat with same key
- **WHEN** Dusty repeats a draft write with the same run, turn and call index key
- **THEN** The server answers the first draft without storing a second draft.

### Requirement: dusty-confirm-draft-window

If the delegation ends between the draft and the confirm-draft, then the server SHALL refuse the confirm-draft.

#### Scenario: Revoke before confirm-draft
- **WHEN** The person revokes the delegation before the confirm-draft
- **THEN** The server refuses the confirm-draft and creates no order.
