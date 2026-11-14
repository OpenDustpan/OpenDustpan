## ADDED Requirements

### Requirement: Queue warning email

When an access token reaches a warning time, the server SHALL queue one warning email row for that token and warning time.

#### Scenario: Warning time becomes due
- **WHEN** an access token reaches a warning time
- **THEN** one row exists for that token and warning time with state queued and due time equal to the warning time

#### Scenario: Send succeeds
- **WHEN** the sender delivers the queued warning
- **THEN** the row moves to sent

#### Scenario: Missing owner address
- **WHEN** the principal has no email at send time
- **THEN** the row moves to failed with reason missing-address and no message leaves the server

### Requirement: Single send under lease

While a warning row is sending, the server SHALL deliver its message at most once under a single sender lease.

#### Scenario: Crash after SMTP accept
- **WHEN** the process ends after SMTP accepts the message and before the row moves to sent
- **THEN** a second sender sends no second message for the same row

#### Scenario: Lease expires during send
- **WHEN** a send holds the row under lease
- **THEN** only the lease holder marks the row sent

### Requirement: Skip stale token

When a queued warning becomes stale before its send, the server SHALL skip the send and record the reason.

#### Scenario: Token revoked or disabled
- **WHEN** the token is revoked, disabled or deleted before the send
- **THEN** the row moves to failed with reason token-not-usable and no message leaves the server

#### Scenario: Expiry changed
- **WHEN** the token expiry changes so the warning time no longer applies
- **THEN** the row moves to failed with reason expiry-changed and no message leaves the server

### Requirement: SMTP TLS and input checks

If the SMTP connection lacks TLS to a non-loopback host, then the server SHALL refuse it unless the plain-SMTP opt-in is on.

#### Scenario: Plain opt-in on
- **WHEN** the plain-SMTP opt-in setting is on
- **THEN** the send proceeds over the allowed plain connection

#### Scenario: Opt-in missing or not valid
- **WHEN** the opt-in value is missing or not valid
- **THEN** the dial refuses, the row moves to retry then failed, and the refusal names the opt-in setting

#### Scenario: Bad sender input
- **WHEN** the relay is empty or the sender holds CR or LF
- **THEN** the adapter refuses with no network call

### Requirement: Retry once and record status

If an SMTP send fails, then the server SHALL retry once and then record failed status with the reason.

#### Scenario: Temporary send failure
- **WHEN** the SMTP send fails
- **THEN** the row moves to retry and the sender tries once more after 15 minutes

#### Scenario: Second failure
- **WHEN** the retry also fails
- **THEN** the row moves to failed with the reason

#### Scenario: SMTP missing
- **WHEN** installation SMTP settings are missing at send time
- **THEN** the row stays queued with reason smtp-not-configured

### Requirement: Secret handling

While handling SMTP credentials, the server SHALL keep the password out of queue rows, logs and audit entries.

#### Scenario: Send fails
- **WHEN** the sender records a failure
- **THEN** the queue row, the log line and the audit entry hold no password

### Requirement: Audit of warning send

When the sender delivers, skips or fails a queued warning, the server SHALL write one audit entry for that warning.

#### Scenario: Warning sent or skipped
- **WHEN** the sender delivers, skips or fails a queued warning
- **THEN** the audit log holds one entry with the token ID, the warning time and the result
