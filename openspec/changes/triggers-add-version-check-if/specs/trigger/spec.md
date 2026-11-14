## ADDED Requirements

### Requirement: Trigger ETag on single read

When a caller reads one trigger, the server SHALL answer the current trigger version as ETag.

#### Scenario: Get one trigger
- **WHEN** a caller reads one trigger
- **THEN** the answer carries the current version as a strong ETag header.

### Requirement: Trigger version on list

When a caller lists the triggers of a unit, the server SHALL answer each item with its current trigger version.

#### Scenario: List triggers
- **WHEN** a caller lists the triggers of a unit
- **THEN** each item carries its version member.

### Requirement: Fresh trigger replace applies

When a caller replaces a trigger with the current If-Match value, the server SHALL apply the replace and answer 200 with the new ETag.

#### Scenario: Fresh If-Match
- **WHEN** a caller replaces a trigger with the current If-Match value
- **THEN** the server raises the version by one, sets the last saver to the caller and answers 200.

### Requirement: Concurrent trigger replace keeps one winner

When two callers replace one trigger with the same If-Match value, the server SHALL refuse one replace with 412.

#### Scenario: Same ETag twice
- **WHEN** two callers replace one trigger with the same If-Match value
- **THEN** one answers 200 and the other answers 412 with the NEW error code `trigger.stale-version`.

### Requirement: Stale trigger replace refused

When a caller replaces a trigger with a stale If-Match value, the server SHALL refuse the replace with 412.

#### Scenario: Stale If-Match
- **WHEN** a caller replaces a trigger with an old If-Match value
- **THEN** the server answers 412 with the NEW error code `trigger.stale-version` and keeps the stored rule and the stored last saver.

### Requirement: Trigger replace needs If-Match

When a caller replaces a trigger without If-Match, the server SHALL answer 428.

#### Scenario: Missing If-Match
- **WHEN** a caller replaces a trigger without If-Match
- **THEN** the server answers 428 with the NEW error code `trigger.if-match-required` and changes nothing.

### Requirement: Trigger replace needs usable If-Match

When a caller replaces a trigger with a malformed, weak, wildcard or multi-value If-Match, the server SHALL refuse the replace with 400.

#### Scenario: Bad If-Match
- **WHEN** a caller sends a weak If-Match
- **THEN** the server answers 400 with the NEW error code `trigger.if-match-invalid` and changes nothing.

### Requirement: Trigger replace after delete

When a caller replaces a trigger that no longer exists, the server SHALL answer 404.

#### Scenario: Deleted trigger
- **WHEN** a caller replaces a trigger that was deleted after the read
- **THEN** the server answers 404 with error code `trigger.not-found`.

### Requirement: Trigger replace denied

When a caller without the replace action replaces a trigger, the server SHALL answer 403.

#### Scenario: Denied caller
- **WHEN** a caller without the replace action sends PUT
- **THEN** the server answers 403 before it checks If-Match.

### Requirement: Other trigger writes keep ETag

When a fire, enable, disable or last-fired write updates a trigger, the server SHALL keep the replace version unchanged.

#### Scenario: Fire keeps version
- **WHEN** a fire updates a trigger
- **THEN** the ETag stays unchanged.
