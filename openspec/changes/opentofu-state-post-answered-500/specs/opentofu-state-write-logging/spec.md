## ADDED Requirements

### Requirement: Failed state POST log names redacted cause

When an OpenTofu state POST answers 500, the server SHALL write one problem-path fault-log line with the redacted wrapped error chain and the request id.

#### Scenario: State POST fails
- **WHEN** an OpenTofu state POST answers 500
- **THEN** the server writes one problem-path fault-log line with the redacted wrapped error chain and the request id of the access line, and writes no second ERROR line

#### Scenario: Redacted chain keeps root cause
- **WHEN** the state POST fails inside the store or blob layer
- **THEN** the log line holds each wrapped layer of the cause and holds no driver connection string, secret, or stack trace

### Requirement: Log cause escapes client text

Where the error chain holds client-controlled text, the server SHALL write it with CR and LF removed and cut at 500 characters.

#### Scenario: Hostile workspace name
- **WHEN** the error chain holds client-controlled text with control characters
- **THEN** the fault-log line holds the text with CR and LF removed and cut at 500 characters
