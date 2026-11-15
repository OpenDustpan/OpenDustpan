## ADDED Requirements

### Requirement: Check and comment

When the plans of a review request finish, the server SHALL publish one summary check, one check for each unit and one edited plan comment.

#### Scenario: Three units
- **WHEN** a push touches three units and their plans finish
- **THEN** the review request has one summary check, three unit checks and one comment

#### Scenario: Second push
- **WHEN** a second push finishes its plans
- **THEN** the same comment shows the new results and no second comment exists

### Requirement: Summary fails

If any unit plan fails, waits on approval or has not finished, then the server SHALL report the summary check as not passed.

#### Scenario: Approval wait
- **WHEN** one unit waits on approval
- **THEN** the summary check does not pass and names that unit

#### Scenario: No change
- **WHEN** a plan has no change
- **THEN** that unit counts as passed

#### Scenario: Zero units
- **WHEN** a push touches no unit
- **THEN** the summary check passes and says no unit changed

### Requirement: Head binding

If the head commit of the review request differs from the commit of the plans, then the server SHALL write no check and no comment.

#### Scenario: Stale event
- **WHEN** a late event names an older head commit
- **THEN** no check or comment changes

### Requirement: Host error

If the Git host does not answer, then the server SHALL fail the write with the answer of the Git host.

#### Scenario: Host down
- **WHEN** the Git host returns an error on a check write
- **THEN** the write fails with that answer, the summary check is not reported as passed and a retry runs later

### Requirement: Comment table

The server SHALL write one table row for each unit with its status, its counts, its resource addresses with their kind of change and a link to the change order.

#### Scenario: Row content
- **WHEN** the renderer builds the comment
- **THEN** each row holds the status, the counts, the addresses with their kind of change and the link, and no value

#### Scenario: Secret value
- **WHEN** a plan holds a secret value
- **THEN** the comment shows no value

#### Scenario: Long list
- **WHEN** the comment text exceeds 60,000 characters
- **THEN** the comment cuts the address list so that the whole comment holds at most 60,000 characters and links to the change order

### Requirement: Comment repair

If the comment of a review request was deleted, then the server SHALL post one new comment.

#### Scenario: Deleted comment
- **WHEN** an edit finds the comment deleted
- **THEN** the server posts one new comment and keeps one comment

#### Scenario: Foreign marker
- **WHEN** a comment by another author holds the marker
- **THEN** the server does not edit it and posts its own comment

#### Scenario: Duplicate marker
- **WHEN** two own comments hold the marker
- **THEN** the server keeps the oldest and deletes the other

### Requirement: Edit order

While an edit of the comment runs, the server SHALL refuse a write with an older version number.

#### Scenario: Slow holder
- **WHEN** a slow edit resumes after a newer edit finished
- **THEN** the older write is refused and the newer comment stays

#### Scenario: Crash after post
- **WHEN** the server stops between the post and the record of the comment ID
- **THEN** the next edit finds the comment by its marker and posts no second comment
