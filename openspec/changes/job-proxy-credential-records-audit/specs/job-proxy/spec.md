## ADDED Requirements

### Requirement: Proxy-only credential injection

When a proxied request matches a rule with a bound credential, the server SHALL add the credential at the job proxy without placing the credential value in the assignment.

#### Scenario: Job without credential value
- **WHEN** a job request matches a rule with a bound credential
- **THEN** the request reaches the upstream host with the credential header and the job holds no credential value.

### Requirement: Sealed credential fail closed

When a credential reference has no valid bound sealed credential, the server SHALL fail the request closed with no network contact.

#### Scenario: Missing bound credential
- **WHEN** a credential reference has no valid bound sealed credential
- **THEN** the proxy sends no network contact and the job log names the credential reference.

### Requirement: Redirect and header strip

When a job request carries its own credential header or answers with a redirect, the server SHALL strip the job header and re-check each redirect host against the effective rule set.

#### Scenario: Forged header and redirect
- **WHEN** a job request carries its own credential header or answers with a redirect
- **THEN** the proxy strips the job header and re-checks the redirect host before it adds the credential.

### Requirement: Response scan

When an upstream response body through the job proxy contains a plain credential value within the scan limit, the server SHALL redact the value before delivery to the job.

#### Scenario: Redacted credential echo
- **WHEN** an upstream response contains a plain credential value
- **THEN** the job receives a redacted body and the record notes a scan finding.

### Requirement: Per-request records

When a caller lists the proxy records of a phase, the server SHALL return one record for each proxied request with host, path, matched rule, decision, and byte counts.

#### Scenario: List phase requests
- **WHEN** a caller lists the proxy records of a phase
- **THEN** the list shows host, path, rule, decision, and byte counts for each request.

### Requirement: Refused request record

When the proxy refuses a request with no allow match, the server SHALL write one refused request record and show blocked by the job proxy with host and path in the job log.

#### Scenario: Refused host recorded
- **WHEN** the proxy refuses a request with no allow match
- **THEN** the phase holds one refused record with host, path, and decision and the job log shows blocked by the job proxy with host and path.

### Requirement: Record before send

When the record write for a proxied request fails, the server SHALL refuse the request with no upstream contact.

#### Scenario: No unrecorded send
- **WHEN** the record write for a proxied request fails
- **THEN** the proxy sends no request and the phase aggregates the failure.

### Requirement: Phase audit entry

When a phase with a job proxy finishes, the server SHALL write one audit entry with proxy facts and the runner session.

#### Scenario: Audited phase
- **WHEN** a phase with a job proxy finishes
- **THEN** the namespace audit log holds one entry with proxy facts and the runner session, including refused-only and zero-request phases.

### Requirement: Plan-phase write block

When a plan phase attempts a write that only an apply rule allows under plan-effective rules, the server SHALL refuse the write before network contact.

#### Scenario: Blocked plan write
- **WHEN** a plan phase attempts a write that only an apply rule allows
- **THEN** the proxy refuses the write and the job log shows blocked by the job proxy with host and path.
