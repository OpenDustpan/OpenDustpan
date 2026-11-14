## ADDED Requirements

### Requirement: Omit blob keys from state errors

When a state blob open or unreferenced-blob cleanup fails, the server SHALL omit the blob key from the error text.

#### Scenario: Malformed key
- **WHEN** a state blob open gets a malformed key
- **THEN** OpenStateBlob returns the malformed-key error without the supplied key

#### Scenario: Failed version lookup
- **WHEN** a state blob open fails its version lookup
- **THEN** OpenStateBlob returns the lookup error without the blob key

#### Scenario: Workspace mismatch
- **WHEN** a state blob belongs to another workspace
- **THEN** OpenStateBlob returns the mismatch error without the blob key

#### Scenario: Cleanup failure
- **WHEN** unreferenced-blob cleanup fails
- **THEN** cleanup returns the failure error without the listed keys and keeps operation, version, and workspace identifiers across the full unwrapped chain
