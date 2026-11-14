## ADDED Requirements

### Requirement: dev-code origin check

When a request arrives at POST sign-in/dev-code with a disallowed origin, the server SHALL refuse the request before any session start.

#### Scenario: Cross-site plain dev-code refused
- **WHEN** A cross-site text/plain request arrives at POST sign-in/dev-code.
- **THEN** Server answers 403 with code sign-in.failed, sets no cookie, and spends no code.

#### Scenario: Missing content type refused
- **WHEN** A request with no Content-Type arrives at POST sign-in/dev-code.
- **THEN** Server answers 403 with code sign-in.failed, sets no cookie, and spends no code.

#### Scenario: Origin refused before decode
- **WHEN** A cross-site text/plain request with an invalid JSON body arrives at POST sign-in/dev-code.
- **THEN** Server answers 403 with code sign-in.failed without decoding the body, sets no cookie, and spends no code.

#### Scenario: Same-site JSON dev-code proceeds
- **WHEN** A same-site request with Content-Type application/json arrives at POST sign-in/dev-code.
- **THEN** Server passes the origin check and continues to the existing dev-code handling.

### Requirement: passkey begin origin check

When a request arrives at POST sign-in/passkey/begin with a disallowed origin, the server SHALL refuse the request before any challenge is issued.

#### Scenario: Cross-site plain passkey begin refused
- **WHEN** A cross-site text/plain request arrives at POST sign-in/passkey/begin.
- **THEN** Server answers 403 with code sign-in.failed, issues no challenge and sets no cookie.

#### Scenario: Same-site JSON passkey begin proceeds
- **WHEN** A same-site request with Content-Type application/json arrives at POST sign-in/passkey/begin.
- **THEN** Server passes the origin check and continues to the existing challenge handling.

### Requirement: passkey finish origin check

When a request arrives at POST sign-in/passkey/finish with a disallowed origin, the server SHALL refuse the request before any session start.

#### Scenario: Cross-site plain passkey finish refused
- **WHEN** A cross-site text/plain request arrives at POST sign-in/passkey/finish.
- **THEN** Server answers 403 with code sign-in.failed, sets no cookie, and consumes no challenge.

#### Scenario: Valid finish succeeds after refused request
- **WHEN** A same-site request with Content-Type application/json and the sealed challenge arrives after a refused cross-site request.
- **THEN** Server consumes the challenge once and starts the session.
