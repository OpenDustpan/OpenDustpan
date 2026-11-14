## ADDED Requirements

### Requirement: follow-from-offset

When a client polls a growing job log in follow mode with a valid byte offset, the server SHALL return the bytes at and after that offset.

#### Scenario: new bytes only
- **WHEN** client polls a growing log with offset 100 after 50 new bytes arrived
- **THEN** server returns 50 bytes plus the next offset 150 and reads zero bytes before offset 100

#### Scenario: offset inside a chunk
- **WHEN** client polls with an offset that falls inside a stored chunk
- **THEN** server returns bytes from the middle of that chunk plus the next offset

#### Scenario: no new bytes
- **WHEN** client polls with an offset equal to the current log length
- **THEN** server returns an empty body with the same next offset

#### Scenario: empty log
- **WHEN** client polls a log that has no chunks yet with offset 0
- **THEN** server returns an empty body with next offset 0

### Requirement: invalid-offset-rejected

If the offset is negative, not a number, overflowing, past the end of the stored log or of a stale generation, then the server SHALL refuse the poll with the NEW error code `job-log.invalid-offset`.

#### Scenario: offset past end refused
- **WHEN** client polls with an offset past the end of the stored log
- **THEN** server answers 400 with the invalid offset error and the current log length

#### Scenario: bad value refused
- **WHEN** client polls with a non-numeric or overflowing offset
- **THEN** server answers 400 with the invalid offset error and does not echo the input

#### Scenario: stale generation refused
- **WHEN** client polls after the log was truncated or redacted and regrew
- **THEN** server answers 400 with the invalid offset error

### Requirement: concurrent-append-committed

While the last chunk grows during a follow poll, the server SHALL return at most 4 megabytes per poll for one job log up to the length committed at the start of that poll.

#### Scenario: growing tail returns committed bytes
- **WHEN** the last chunk grows while one poll reads it
- **THEN** server returns bytes up to the length committed at the poll start, the next offset equals that length, and no torn bytes appear
