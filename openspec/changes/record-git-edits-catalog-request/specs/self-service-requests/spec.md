## ADDED Requirements

### Requirement: Record git drift

When a push delivery on the home ref carries a request file that differs from the stored request document, the server SHALL mark the request as drifted with the commit id and the changed field names.

#### Scenario: Push edits inputs
- **WHEN** a push delivery on the home ref carries a request file whose inputs differ from the stored document
- **THEN** the request shows drifted with the commit id and the changed field names, and the stored document is unchanged

### Requirement: Push matches stored document

When a push delivery on the home ref carries a request file equal to the stored request document, the server SHALL clear the drift to in-sync.

#### Scenario: Matching push clears drift
- **WHEN** a push delivery on the home ref carries a request file equal to the stored request document
- **THEN** the request shows in-sync with the commit id and time of the matching push

### Requirement: Reject forged delivery

When a push delivery carries a bad webhook signature, the server SHALL answer 401 and write no drift record.

#### Scenario: Bad signature writes nothing
- **WHEN** a push delivery carries a bad webhook signature
- **THEN** the server answers 401 and writes no drift record

### Requirement: Refuse oversize delivery

When a push delivery is larger than the size cap, the server SHALL answer 413 and write no drift record.

#### Scenario: Oversize delivery refused
- **WHEN** a push delivery is larger than the size cap
- **THEN** the server answers 413 and writes no drift record

### Requirement: Record invalid git file

When a push delivery carries a request file that fails strict parse or path checks, the server SHALL record invalid-file status with the field error and answer 202.

#### Scenario: Malformed file becomes visible status
- **WHEN** a push delivery carries a request file that fails strict parse or path checks
- **THEN** the webhook answers 202 and the request shows invalid-file with the field error and the commit id

### Requirement: Record removed git file

When a push delivery deletes or renames the request file in git, the server SHALL mark the request as file-removed with the commit id.

#### Scenario: Deleted file becomes visible status
- **WHEN** a push delivery deletes or renames the request file in git
- **THEN** the request shows file-removed with the commit id and keeps the stored document

### Requirement: Ignore out-of-scope delivery

When a push delivery targets a ref other than the home ref or names an unknown request or a path and document mismatch, the server SHALL write no drift record.

#### Scenario: Feature branch ignored
- **WHEN** a push delivery targets a ref other than the home ref
- **THEN** the server writes no drift record

#### Scenario: Repository mismatch ignored
- **WHEN** a push delivery names a repository other than the home repository of the request
- **THEN** the server writes no drift record

### Requirement: Order deliveries by commit

When a delivery carries a commit that is an ancestor of the recorded commit, the server SHALL keep the recorded commit.

#### Scenario: Stale commit ignored
- **WHEN** a delivery carries a commit that is an ancestor of the recorded commit
- **THEN** the drift record keeps the newer commit

### Requirement: Refuse write over drift

When a write-back starts while the request shows drifted, invalid-file, or file-removed, the server SHALL refuse the write with 409.

#### Scenario: Write-back over drift fails
- **WHEN** a write-back starts while the request shows drifted, invalid-file, or file-removed
- **THEN** the server answers 409 with code request.drifted and changes neither git nor the stored document

#### Scenario: Concurrent mark blocks write
- **WHEN** a push marks drift after the write-back check and before its git write
- **THEN** the write-back answers 409 and changes neither git nor the stored document

### Requirement: Repeated delivery

When a push delivery repeats a processed delivery id, the server SHALL ignore the repeat and answer 202.

#### Scenario: Redelivery is idempotent
- **WHEN** a push delivery repeats a processed delivery id
- **THEN** the drift record keeps the first result and the answer repeats 202

### Requirement: Recover missed push at start

When the server starts, the server SHALL read the current file at the request path of each request and update its drift record.

#### Scenario: Start catches up
- **WHEN** the server starts after a push that no webhook delivered
- **THEN** the server reads the current file at the request path and updates the drift record

### Requirement: Read drift status

While a request has a drift record, the server SHALL answer its status, commit id, changed fields, and time to a principal with self-service:read.

#### Scenario: Authorized reader sees drift
- **WHEN** a principal with self-service:read reads the drift of a request by stable request ID
- **THEN** the answer lists status, commit id, changed fields, and time

### Requirement: Deny drift without read

When a principal without self-service:read reads the drift of a request, the server SHALL deny the read.

#### Scenario: Reader without rights denied
- **WHEN** a principal without self-service:read reads the drift of a request
- **THEN** the server denies the read and names the missing action

### Requirement: Delete drifted request

When an authorized principal deletes a request through the API while drift shows, the server SHALL remove the stored request and its drift record together.

#### Scenario: API delete clears drift
- **WHEN** an authorized principal deletes a request through the API while drift shows
- **THEN** the stored request and its drift record are both removed

### Requirement: Audit drift mark and clear

When the server marks or clears drift for a request, the server SHALL write one audit entry with the commit id.

#### Scenario: Mark and clear audited
- **WHEN** the server marks drift and later clears it for one request
- **THEN** the audit log holds one entry for the mark and one entry for the clear with the commit id and delivery id
