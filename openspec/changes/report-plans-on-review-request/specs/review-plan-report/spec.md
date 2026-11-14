## ADDED Requirements

### Requirement: Check and comment

When the plans of a review request finish, the server SHALL publish one summary check and one edited plan comment.

#### Scenario: Three units
- **WHEN** a push touches three units and their plans finish
- **THEN** the review request has one summary check and one comment

#### Scenario: Second push
- **WHEN** a second push finishes its plans
- **THEN** the same comment shows the new results and no second comment exists

### Requirement: Summary fails

If any unit plan fails, waits on approval or has not finished, then the server SHALL report the summary check as not passed.

#### Scenario: Approval wait
- **WHEN** one unit waits on approval
- **THEN** the summary check does not pass and names that unit

### Requirement: Host error

If the Git host does not answer, then the server SHALL fail the write with the answer of the Git host.

#### Scenario: Host down
- **WHEN** the Git host returns an error on a check write
- **THEN** the write fails with that answer and the summary check is not reported as passed

### Requirement: Comment table

The server SHALL write one table row for each unit with its status, its counts, its resource addresses with their kind of change and a link to the change order.

#### Scenario: Row content
- **WHEN** the renderer builds the comment
- **THEN** each row holds the status, the counts, the addresses with their kind of change and the link, and no value

#### Scenario: Secret value
- **WHEN** a plan holds a secret value
- **THEN** the comment shows no value

### Requirement: Comment repair

If the comment of a review request was deleted, then the server SHALL post one new comment.

#### Scenario: Deleted comment
- **WHEN** an edit finds the comment deleted
- **THEN** the server posts one new comment and keeps one comment

### Requirement: Edit order

While an edit of the comment runs, the server SHALL hold one lock for the review request.

#### Scenario: Quick pushes
- **WHEN** two pushes finish their plans at the same time
- **THEN** the edits run one after the other and one comment exists

#### Scenario: Crash after post
- **WHEN** the server stops between the post and the record of the comment ID
- **THEN** the next edit finds the comment by its marker and posts no second comment
