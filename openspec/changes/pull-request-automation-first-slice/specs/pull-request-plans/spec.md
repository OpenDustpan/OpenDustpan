## ADDED Requirements

### Requirement: Composed first slice

When a push arrives for an open review request, the server SHALL produce plan jobs with one commit status and one summary comment through the composed intake, trigger, and report changes.

#### Scenario: Push yields plans, one status, one comment
- **WHEN** a push arrives for an open review request
- **THEN** one plan job runs for each touched unit and the review request shows one commit status and one summary comment

### Requirement: New push edits same comment

When a new push arrives for the same review request, the server SHALL show the new plan summary in the same edited comment.

#### Scenario: Same comment edited
- **WHEN** a new push arrives for the same review request
- **THEN** the same summary comment shows the new plan summary with no added comment
