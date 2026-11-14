## ADDED Requirements

### Requirement: Open static profile

When a caller with profile:open opens a static profile, the server SHALL answer the merged values with their secret and never-store marks.

#### Scenario: Open merged values
- **WHEN** a caller with profile:open opens a static profile with imports, final entries and checks
- **THEN** the server answers merged values with marks and writes one access entry with no value

#### Scenario: Refused open
- **WHEN** a caller lacks profile:open on an imported profile
- **THEN** the server answers 403 naming the first unreadable profile and writes one access entry with no value

#### Scenario: Never-store refused
- **WHEN** consumer pulumi or mode data asks a never-store leaf
- **THEN** the server refuses with the path and names the ephemeral resource

### Requirement: Refuse provider blocks

When the resolve reaches a provider block, the server SHALL fail the open with an error that names the block.

#### Scenario: Block reached
- **WHEN** the resolve reaches a provider block
- **THEN** the open fails naming the block

### Requirement: Replay an open

When a caller repeats an open with the same Idempotency-Key, the server SHALL answer the stored session ID.

#### Scenario: Replay key
- **WHEN** a caller repeats an open with the same key within 24 hours, for one principal
- **THEN** the server answers the same session ID and stores 1 session

#### Scenario: Replay conflict
- **WHEN** a caller replays a key with another body or principal
- **THEN** the server answers conflict and stores nothing new
