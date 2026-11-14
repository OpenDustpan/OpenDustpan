## ADDED Requirements

### Requirement: Typed reason required

When a break-glass account starts a sign-in, the server SHALL refuse the sign-in without a typed reason of 1 to 500 characters with no control character.

#### Scenario: Empty reason refused
- **WHEN** a break-glass account starts a sign-in with no typed reason
- **THEN** the server refuses the sign-in and sets no session cookie

#### Scenario: Controls refused
- **WHEN** the reason holds a CR, LF, tab, DEL or Unicode line separator
- **THEN** the server refuses the sign-in and sets no session cookie

#### Scenario: Long reason refused
- **WHEN** the reason has 501 characters
- **THEN** the server refuses the sign-in and sets no session cookie

#### Scenario: Limits accepted
- **WHEN** the reason has 1 or 500 printable characters, including non-ASCII letters
- **THEN** the server accepts the reason

### Requirement: Alert on sign-in

When a break-glass sign-in succeeds, the server SHALL write the alert audit entry in the transaction of the sign-in.

#### Scenario: Alert written
- **WHEN** a break-glass sign-in with a typed reason succeeds
- **THEN** the audit log holds one entry with the reason and the server then writes one WARN log line

#### Scenario: Audit failure
- **WHEN** the audit write fails during the sign-in
- **THEN** the server refuses the sign-in and issues no session

#### Scenario: Log write lost
- **WHEN** the WARN log write fails after the commit
- **THEN** the session stays valid and the audit entry stays in the log

### Requirement: Session time limit

When a request arrives at or after the time limit of a break-glass session, the server SHALL deny the request with 401 and write an audit entry.

#### Scenario: Expiry
- **WHEN** a request arrives after the time limit
- **THEN** the server answers 401 and writes an audit entry

#### Scenario: Restart
- **WHEN** the server restarts before the limit
- **THEN** the session stays valid until the limit

### Requirement: Limit from the setting

Where the installation sets the break-glass session limit, the server SHALL issue each break-glass session with an expiry not later than that limit.

#### Scenario: Setting applied
- **WHEN** a site administrator sets the limit and a break-glass sign-in succeeds
- **THEN** the expiry is not later than the sign-in time plus the limit

#### Scenario: Effective value shown
- **WHEN** no one sets the limit
- **THEN** the settings API shows the effective default value

### Requirement: Expiry bound

If the expiry of a break-glass cookie is later than the limit from the server clock, then the server SHALL deny the request.

#### Scenario: Future expiry
- **WHEN** a cookie carries an expiry beyond the limit ahead of the server clock
- **THEN** the server answers 401 and writes an audit entry

### Requirement: Per-request audit

While a break-glass session is active, the server SHALL write one audit entry for each request with the session ID and the reason.

#### Scenario: Session actions recorded
- **WHEN** a request arrives with a break-glass session
- **THEN** the audit entry names the session and the reason

### Requirement: Request audit failure closed

If the audit write of a request of a break-glass session fails, then the server SHALL answer the request with 503.

#### Scenario: Refused request
- **WHEN** the audit store is down during a request
- **THEN** the server answers 503 and the request changes no data

### Requirement: No identity provider start

If a break-glass sign-in starts at the identity provider, then the server SHALL refuse the sign-in.

#### Scenario: IdP start refused
- **WHEN** an identity provider assertion names a break-glass account
- **THEN** the server issues no session

### Requirement: Early end of a session

When a site administrator revokes the sessions of a break-glass principal, the server SHALL deny each session of that principal at its next request.

#### Scenario: Early end
- **WHEN** a site administrator adds 1 to the session generation of the principal
- **THEN** the next request of the session answers 401

### Requirement: Floor of two paths kept

While a break-glass sign-in or session exists, the server SHALL keep the count of enabled break-glass accounts unchanged.

#### Scenario: No change of the count
- **WHEN** a break-glass sign-in succeeds or its session ends
- **THEN** the count of enabled break-glass accounts stays the same

### Requirement: 90-day test marker

If a break-glass account has no successful sign-in for 90 days, then the server SHALL set a not-tested flag in the principal response.

#### Scenario: Fresh at 89 days
- **WHEN** the last successful sign-in is 89 days old
- **THEN** the flag is false

#### Scenario: Stale at 91 days
- **WHEN** the last successful sign-in is 91 days old
- **THEN** the principal response shows the last test time and the flag true

#### Scenario: Refused sign-in
- **WHEN** a sign-in is refused
- **THEN** the last test time does not change

#### Scenario: Never signed in
- **WHEN** an account made 91 days ago has never signed in
- **THEN** the principal response shows no test time and the flag true
