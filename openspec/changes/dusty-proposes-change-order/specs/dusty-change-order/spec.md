## ADDED Requirements

### Requirement: dusty-writes-a-draft-under-a-delegation

When Dusty acts under a live delegation that covers the unit, the server SHALL store a draft that names the person, the agent and the delegation, and SHALL NOT treat the draft as a change order.

#### Scenario: draft-under-a-live-delegation
- **WHEN** Dusty writes a draft for a unit that the delegation covers
- **THEN** the draft names the person, the agent and the delegation, and no change order exists

### Requirement: ended-or-uncovering-delegation-refuses-the-draft

If the delegation has ended or does not cover the unit, then the server SHALL refuse the draft and SHALL store nothing.

#### Scenario: ended-delegation
- **WHEN** Dusty drafts after the delegation ended
- **THEN** the server refuses the draft with `delegation.ended` and stores nothing

#### Scenario: unit-not-covered
- **WHEN** Dusty drafts for a unit that the delegation does not cover
- **THEN** the server refuses the draft and stores nothing

### Requirement: draft-once-for-one-key

When Dusty repeats a draft write with the same run, turn and call index key, the server SHALL return the first draft and SHALL NOT store a second draft.

#### Scenario: repeat-with-the-same-key
- **WHEN** Dusty repeats a draft write with the same key
- **THEN** the server answers the first draft and stores no second draft

### Requirement: draft-records-no-approval

The server SHALL NOT record an approval from a draft or from an agent.

#### Scenario: no-approval
- **WHEN** Dusty writes a draft
- **THEN** the draft holds no approval
