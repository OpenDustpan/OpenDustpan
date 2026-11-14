## ADDED Requirements

### Requirement: fan-out history cause

When a plan job or apply job created by terragrunt run-all fan-out writes a history entry, the server SHALL store the discovery job ID, the requesting principal stable ID, and the fixed reason value in that entry.

#### Scenario: Apply history names discovery, requester, and reason
- **WHEN** a plan job or apply job created by terragrunt run-all fan-out writes its history entry
- **THEN** the history entry carries the stable ID of the discovery job, the stable ID and subject copy of the principal that requested the wrapper job, and the fixed reason value.

### Requirement: fan-out audit cause

When a plan job or apply job created by terragrunt run-all fan-out writes an audit entry, the server SHALL store the discovery job ID, the requesting principal stable ID, and the fixed reason value in that entry.

#### Scenario: Audit entry names discovery, requester, and reason
- **WHEN** a plan job or apply job created by terragrunt run-all fan-out writes its audit entry
- **THEN** the audit entry carries the stable ID of the discovery job, the stable ID and subject copy of the principal that requested the wrapper job, and the fixed reason value.

### Requirement: history cause readable

Where a caller with the existing unit read grant reads a fan-out history entry, the server SHALL return the stored discovery job ID, requesting principal ID with subject copy, and fixed reason.

#### Scenario: History read returns stored cause
- **WHEN** a caller with the existing unit read grant reads a fan-out history entry
- **THEN** the read returns the stored discovery job ID, requesting principal ID with subject copy, and fixed reason.

### Requirement: audit cause readable

Where a caller with the existing audit-read grant reads a fan-out audit entry, the server SHALL return the stored discovery job ID, requesting principal ID with subject copy, and fixed reason.

#### Scenario: Audit read returns stored cause
- **WHEN** a caller with the existing audit-read grant reads a fan-out audit entry through the existing audit read
- **THEN** the read returns the stored discovery job ID, requesting principal ID with subject copy, and fixed reason.

### Requirement: stable identity across rename

Where fan-out provenance is stored, the server SHALL reference the discovery job and the requesting principal by stable ID rather than by name.

#### Scenario: Principal rename keeps cause link
- **WHEN** the requesting principal is renamed after a fan-out run
- **THEN** the history and audit entries still resolve to the same discovery job ID and the same requesting principal ID.

### Requirement: deleted principal keeps cause

When the requesting principal is removed after a fan-out run, the server SHALL keep the stored discovery job ID and requesting principal ID with the subject copy from write time on each fan-out entry.

#### Scenario: Removed principal still resolves
- **WHEN** the requesting principal is later removed
- **THEN** the entries still carry the stored discovery job ID and requesting principal ID with the subject copy from write time under the same read grants with no erasure.

### Requirement: service requester without subject

When the requesting principal has no subject, the server SHALL store the discovery job ID, the requesting principal ID, an empty subject copy, and the fixed reason.

#### Scenario: Runner requester stores empty subject copy
- **WHEN** the requesting principal has no subject
- **THEN** the entries carry the discovery job ID, the requesting principal ID, an empty subject copy, and the fixed reason.

### Requirement: discovery job has no cause

When the discovery job writes its audit entry, the server SHALL omit cause fields from that entry.

#### Scenario: Discovery audit carries no cause
- **WHEN** the discovery job writes its audit entry
- **THEN** its audit entry carries no cause fields and no history entry exists for it.

### Requirement: non-fan-out entries omit cause

When a plan job or apply job not created by terragrunt run-all fan-out writes entries, the server SHALL omit cause fields from those entries.

#### Scenario: Local apply has no cause
- **WHEN** a plan job or apply job not created by fan-out writes its entries
- **THEN** the history and audit entries carry no cause fields.

### Requirement: retry keeps cause

When the same fan-out plan job or apply job ID executes again, the server SHALL keep the same discovery job ID, requesting principal ID, and fixed reason on each written entry.

#### Scenario: Re-execution keeps same cause
- **WHEN** the same fan-out plan job or apply job ID executes again
- **THEN** each written history and audit entry carries the same discovery job ID, requesting principal ID, and fixed reason.

### Requirement: server sets cause

When a job request includes client-supplied cause fields, the server SHALL ignore those fields and store only the server-computed cause.

#### Scenario: Forged cause is ignored
- **WHEN** a job request includes client-supplied cause fields
- **THEN** the stored entry carries the server-computed discovery job ID and requesting principal ID.

### Requirement: cause binding

When a child job names a discovery job that is unknown, in another namespace, or not a discovery job, the server SHALL refuse the child job with the NEW error code job.cause-invalid.

#### Scenario: Foreign discovery reference is refused
- **WHEN** a child job names a discovery job that is unknown, in another namespace, or not a discovery job
- **THEN** the server refuses the child job with the NEW error code job.cause-invalid and writes no history or audit entry.

### Requirement: partial cause reads as absent

When a history or audit row has any cause field null or empty, the server SHALL return no cause fields for that row.

#### Scenario: Partial row shows no cause
- **WHEN** a history or audit row has any cause field null or empty
- **THEN** the read returns no cause fields.
