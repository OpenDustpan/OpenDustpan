## ADDED Requirements

### Requirement: Member add needs cover

When a member add gives a principal an act-as path through a group that holds principal:manage-tokens, the server SHALL refuse the add unless the reach of the caller covers the reach of each target principal at the time of the add.

#### Scenario: Non-covering add refused
- **WHEN** the caller reach does not cover a target of the group
- **THEN** the server answers 403 and adds no membership

#### Scenario: Covering add allowed
- **WHEN** the caller reach covers each target of the group
- **THEN** the server adds the membership

#### Scenario: Concurrent add
- **WHEN** two adds run at the same time and one invalidates the cover of the other
- **THEN** the server refuses the add that no longer has cover

### Requirement: Write access protects a group

Where a group holds write access to every state, the server SHALL treat the group as protected.

#### Scenario: Group becomes protected
- **WHEN** a grant gives write access to every state to a group
- **THEN** the server keeps the grant, protects the group and flags the edge
