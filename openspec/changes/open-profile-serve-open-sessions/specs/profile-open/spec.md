## ADDED Requirements

### Requirement: Open static profile

When a caller with profile:open opens a static profile, the server SHALL answer the merged values with their secret and never-store marks.

#### Scenario: Open merged values
- **WHEN** A caller with profile:open opens a static profile with imports, final entries, and checks.
- **THEN** The server answers merged values with secret and never-store marks and writes one access entry with no value in the same transaction as the open.

#### Scenario: Crash atomicity
- **WHEN** The server crashes between session put and audit write.
- **THEN** Neither the session nor the entry appears.

#### Scenario: Refused open
- **WHEN** A caller lacks profile:open on an imported profile.
- **THEN** The server refuses with the first unreadable profile and writes one access entry with no value.

#### Scenario: Never-store for pulumi and data
- **WHEN** Consumer pulumi or open mode data asks an exports leaf marked never store.
- **THEN** The server refuses with the path and names the ephemeral resource.

### Requirement: Refuse provider blocks

When the resolve reaches a provider block, the server SHALL fail the open with profile.ErrProvider naming the block.

#### Scenario: Block reached
- **WHEN** The resolve reaches a provider block.
- **THEN** The open fails with profile.ErrProvider naming the block.

### Requirement: Idempotent open replay

When a caller repeats an open with the same Idempotency-Key, the server SHALL answer the stored session ID.

#### Scenario: Replay key
- **WHEN** A caller repeats an open with the same Idempotency-Key, including concurrent replays.
- **THEN** The server answers the same session ID and stores one session.

#### Scenario: Replay after delete
- **WHEN** A caller replays a key whose session was swept, ended, or deleted after revoke or refusal.
- **THEN** The replay answers the stored session ID while the session read answers 404 or 403 with no value.

### Requirement: Open limits

When an open passes its revision, call, time, or size limit, the server SHALL fail the open with the breached limit.

#### Scenario: Limit breach
- **WHEN** An open passes 64 revisions, 128 provider calls, 60 seconds, or a 4 MiB result.
- **THEN** The open fails with the breached limit and writes one access entry with no value.
