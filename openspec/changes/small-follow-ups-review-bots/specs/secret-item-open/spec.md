## ADDED Requirements

### Requirement: close-clears

When the caller closes the opened reader, the server SHALL zero the opened key copy.

#### Scenario: close-without-read
- **WHEN** the caller opens content and closes without reading
- **THEN** the clear hook fires once

#### Scenario: close-after-full-read
- **WHEN** the caller reads all content and then closes
- **THEN** the key copy is zeroed and a second close succeeds

#### Scenario: close-concurrent-with-read
- **WHEN** the caller closes concurrent with a read
- **THEN** the key is zeroed once and the read ends with content or the fixed sealed-read error

### Requirement: read-after-close

When the caller reads after close, the server SHALL return the fixed sealed-read error.

#### Scenario: close-twice-read-after-close
- **WHEN** the caller closes twice and then reads
- **THEN** the second close succeeds and the read returns the fixed sealed-read error

### Requirement: context-end-clears

When the open context ends before close, the server SHALL zero the opened key copy.

#### Scenario: context-end
- **WHEN** the open context ends before close
- **THEN** the key copy is zeroed and each read fails with no content

#### Scenario: context-end-during-read
- **WHEN** the open context ends while a read is in progress
- **THEN** the key is zeroed and the read returns the fixed sealed-read error
