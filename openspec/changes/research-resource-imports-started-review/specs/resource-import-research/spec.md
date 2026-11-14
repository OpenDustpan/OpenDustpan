## ADDED Requirements

### Requirement: research-note-sources

Where resource imports started from a review request are studied, the server SHALL publish a research note that lists primary sources per issue and failure cases.

#### Scenario: hostile-import-block
- **WHEN** a fork review request holds an import block
- **THEN** the note names the hostile import block scenario and states how runner identity and profile credentials limit it

#### Scenario: already-managed-resource
- **WHEN** the imported address is already managed by another unit
- **THEN** the note states that the plan shows the conflict and the job stops without writing state

#### Scenario: concurrent-apply
- **WHEN** two applies target the same unit at the same time
- **THEN** the note states that one apply wins and the other waits or fails with a lock conflict

#### Scenario: failed-import-after-merge
- **WHEN** an import fails after merge
- **THEN** the note states that the plan is out of date, the job fails without writing state, and the change order shows the failed apply

### Requirement: research-note-conflicts

Where post-merge apply is recommended, the server SHALL publish a research note that quotes owner decisions 27.4, 27.7, 42.1 and 6.4 and states fit or conflict for each.

#### Scenario: wrong-address
- **WHEN** the import address is wrong or rewritten
- **THEN** the note states the preview shows the wrong address and approval does not proceed

#### Scenario: finding-conflict
- **WHEN** an import overlaps an action of a finding
- **THEN** the note states how the finding action and the review-request start interact

#### Scenario: closed-review-request
- **WHEN** the review request closes or force-pushes during the job
- **THEN** the note states that the job stops or re-plans after merge or force-push
