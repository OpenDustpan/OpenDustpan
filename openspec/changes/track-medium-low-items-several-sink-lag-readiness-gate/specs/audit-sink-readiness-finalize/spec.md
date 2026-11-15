## ADDED Requirements

### Requirement: Finalize-only retry

When OpenTofu finalize meets a transient store error, the server SHALL retry through FinalizeOpenTofuRetry with no retry outside finalize.

#### Scenario: Finalize retries
- **WHEN** FinalizeOpenTofuRetry meets a transient store error in finalize
- **THEN** OpenTofu write retries in finalize with backoff

#### Scenario: No early retry
- **WHEN** the same error happens outside finalize
- **THEN** server returns the error with no retry
