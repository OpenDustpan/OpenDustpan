## ADDED Requirements

### Requirement: Safe headers

When a notification address or subject holds a control character, the server SHALL refuse the message build and send nothing.

#### Scenario: Header injection refused
- **WHEN** a subject or address holds CR, LF, or a non-printable character
- **THEN** the server refuses the build with the NEW error code smtp.header-invalid and sends nothing

### Requirement: Recipient shape

When a recipient address has an invalid shape, the server SHALL refuse the message build and send nothing.

#### Scenario: Bad recipient refused
- **WHEN** a recipient address or display name holds an invalid shape or CRLF
- **THEN** the server refuses the build with the NEW error code smtp.recipient-invalid and sends nothing

### Requirement: Safe body

When the server builds notification mail, the server SHALL encode it as plain text with no remote content.

#### Scenario: Body stays text only
- **WHEN** notice content holds HTML or an image reference
- **THEN** the recipient sees plain text with no remote load

### Requirement: Non-ASCII subject

When a subject holds non-ASCII text, the server SHALL encode it with RFC 2047 encoded words.

#### Scenario: UTF-8 subject encoded
- **WHEN** a subject holds non-ASCII text within 78 characters
- **THEN** the server sends the subject as RFC 2047 encoded words within 998 characters per line

### Requirement: Send bounds

When a send exceeds 50 recipients or 1 MB, the server SHALL refuse it before it dials the relay.

#### Scenario: Oversize send refused
- **WHEN** a send holds more than 50 recipients or more than 1 MB
- **THEN** the server refuses before dial with the NEW error code smtp.limits-exceeded

### Requirement: Send timeout

When dial or send exceeds 10 seconds, the server SHALL stop the attempt and send nothing.

#### Scenario: Stalled relay times out
- **WHEN** dial or send exceeds 10 seconds
- **THEN** the server stops the attempt with the NEW error code smtp.timeout and sends nothing

### Requirement: Fixed sender

When the server sends notification mail, the server SHALL use the configured installation sender only.

#### Scenario: Caller sender ignored
- **WHEN** a caller offers another sender address
- **THEN** the server sends from the configured installation sender only

### Requirement: Credential failure

When the relay refuses the credentials, the server SHALL answer the NEW error code smtp.credentials-refused.

#### Scenario: Bad credentials map
- **WHEN** the relay refuses the credentials
- **THEN** the server answers the NEW error code smtp.credentials-refused with no secret in the text

### Requirement: Recipient failure

When the relay refuses a recipient, the server SHALL answer the NEW error code smtp.recipient-refused and send to no recipient of that send.

#### Scenario: Bad recipient maps
- **WHEN** the relay refuses a recipient
- **THEN** the server answers the NEW error code smtp.recipient-refused and sends to no partial subset

### Requirement: Redacted errors

When a send fails at the relay, the server SHALL repeat no secret and no full relay reply in the error text.

#### Scenario: Relay reply redacted
- **WHEN** a send fails at the relay
- **THEN** the error repeats no secret and no full relay reply

### Requirement: Refused send recorded

When a build refusal or send failure occurs, the server SHALL send nothing, drop the notice, and write an audit record of the refusal.

#### Scenario: Refusal drops and records
- **WHEN** a build refusal or send failure occurs
- **THEN** the server sends nothing, drops the notice, and writes an audit record of the refusal
