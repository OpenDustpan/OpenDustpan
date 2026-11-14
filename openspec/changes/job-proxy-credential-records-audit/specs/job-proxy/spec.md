## ADDED Requirements

### Requirement: Proxy-only credential injection

When a proxied request matches a rule with a bound credential, the server SHALL add the credential at the job proxy without placing the credential value in the assignment.

#### Scenario: Job without credential value
- **WHEN** a job request matches a rule with a bound credential
- **THEN** the request reaches the upstream host with the credential header and the job holds no credential value.

### Requirement: Sealed credential fail closed

If a credential reference has no valid bound sealed credential, then the server SHALL fail the request closed with no network contact.

#### Scenario: Missing bound credential
- **WHEN** a credential reference has no valid bound sealed credential
- **THEN** the proxy sends no network contact and the job log names the reference.

#### Scenario: Revoked during phase
- **WHEN** a bound credential is revoked while a phase runs
- **THEN** the proxy refuses the request with no network contact and writes one refused record.

### Requirement: Redirect and header strip

When a job request carries its own credential header or meets a redirect, the server SHALL strip the job header and re-check each redirect host against the effective rule set.

#### Scenario: Forged header and redirect
- **WHEN** a job request carries its own credential header and meets a redirect
- **THEN** the proxy strips the header and re-checks the redirect host before it adds the credential.

### Requirement: Response scan

When an upstream response body contains a plain credential value within the scan limit, the server SHALL redact the value before delivery to the job.

#### Scenario: Redacted credential echo
- **WHEN** an upstream response contains a plain credential value within the scan limit
- **THEN** the job receives a redacted body and the record notes a scan finding.

### Requirement: Unscannable response fail closed

If an upstream response exceeds 1 MiB or the proxy cannot scan the body, then the server SHALL refuse the body with no delivery to the job.

#### Scenario: Body over scan limit
- **WHEN** an upstream response exceeds 1 MiB or the proxy cannot scan the body
- **THEN** the proxy refuses the body with no delivery to the job and writes one refused record.

### Requirement: Per-request records

When a caller with unit read grant lists the proxy records of a phase (list-records), the server SHALL return one record for each proxied request with host, path, matched rule, decision and byte counts.

#### Scenario: List phase requests
- **WHEN** a caller with unit read grant lists the proxy records of a phase
- **THEN** the list shows host, path, rule ID, rule set revision, decision and byte counts for each request.

#### Scenario: Denied list call
- **WHEN** a caller without unit read grant lists the proxy records of a phase
- **THEN** the server denies the call with 403.

### Requirement: Record content without query

When a proxied request carries a query string, the server SHALL store host plus path without the query string and without any credential value.

#### Scenario: Query excluded
- **WHEN** a proxied request carries a query string
- **THEN** the record holds host plus path with no query string and no credential value.

### Requirement: Refused request record

When the job proxy refuses a request with no allow match, the server SHALL write one refused record and show blocked by the job proxy with host and path in the job log.

#### Scenario: Refused host recorded
- **WHEN** the proxy refuses a request with no allow match
- **THEN** the phase holds one refused record and the job log shows host and path.

### Requirement: Record before send

If the record write for a proxied request fails, then the server SHALL refuse the request with no upstream contact.

#### Scenario: No unrecorded send
- **WHEN** the record write for a proxied request fails
- **THEN** the proxy sends no request and the phase aggregates the failure.

### Requirement: Phase audit entry

When a phase with a job proxy finishes (finish-phase), the server SHALL write one audit entry with proxy facts and the runner session.

#### Scenario: Audited phase
- **WHEN** a phase with a job proxy finishes
- **THEN** the audit log holds one entry with proxy facts and the runner session, also for a phase with no request.

### Requirement: Idempotent phase audit

When the phase finish runs again with the same phase ID, the server SHALL keep one audit entry for that phase ID.

#### Scenario: Retried finish
- **WHEN** the phase finish runs again with the same phase ID
- **THEN** the audit log still holds one entry for the phase ID.

### Requirement: Plan-phase write block

When a plan phase attempts a write that only an apply rule allows, the server SHALL refuse the write before network contact.

#### Scenario: Blocked plan write
- **WHEN** a plan phase attempts a write that only an apply rule allows
- **THEN** the proxy refuses the write and the job log shows blocked by the job proxy with host and path.

### Requirement: Plan write definition

When a plan phase sends a request, the server SHALL decide write from the HTTP method and the effective rule set revision.

#### Scenario: POST read still checked
- **WHEN** a plan phase sends a POST against the plan-effective rule set revision
- **THEN** the proxy applies the plan-effective rule set of that revision.

### Requirement: Credential binding stable ID

When a proxy rule binds a credential, the server SHALL bind the stable credential ID and not the credential name.

#### Scenario: Renamed credential still binds
- **WHEN** a bound credential is renamed
- **THEN** the rule still resolves the same credential.
