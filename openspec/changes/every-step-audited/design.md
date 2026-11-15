## Context

Agent steps use the same REST API as a person. The audit log stores per-namespace entries with a hash chain plus an installation scope. The agent block of audit-entry-has-no-agent supplies the agent field. This change stores the delegation ID and the reason on each entry of a delegated step, and verifies the chain over those fields.

## Decisions

1. **Stored delegation and reason fields on the audit entry.** Reuse the agent block of audit-entry-has-no-agent for the agent field and store only the NEW fields delegation ID and reason here. Store them at write time, in the same transaction as an allowed step. A denied step writes a denial entry in its own transaction. If the denial append fails the call is still refused with 503. Readers get the fields from the stored entry, never from a join. Each entry stores a hash-input version: version 1 for old entries, version 2 for new entries with delegation and reason. The version is part of the hash input.
2. **Reason input, limit and error.** The caller supplies the reason as the body field reason on each delegated tool call, and it is required every time. The reason holds at most 500 Unicode code points, each an ASCII printable character 0x20 to 0x7E. The server refuses a missing, long, or out-of-allowlist reason with the NEW error code audit.reason-invalid, with no entry written, and repeats nothing of the input. Transcript text never enters the reason. A forged or unknown delegation ID is never stored raw: the denial entry stores the literal unknown-delegation plus at most 64 ASCII printable characters of the input.

## Structure

The unexported type auditEntry with a constructor that requires subject, agent, delegation ID, action, object and reason makes an entry without delegation data impossible to build.

## Risks

- Transcript text stays out of the audit entry; the entry holds only the reason and the references.
- A forged delegation ID is stored bounded and never raw, so it cannot fill the log.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service refuses a delegated call with no delegation ID, an unknown, revoked or expired delegation ID, or no valid reason, and writes a denial entry that names the delegation where known and the refusal cause alongside the agent block of audit-entry-has-no-agent. A call refused for audit.reason-invalid writes no entry. | Send a delegated tool call with an empty, a forged, a revoked and a replayed delegation ID directly to the service and check that each denies, that reason-invalid writes no entry, and that other denials each write one denial entry. |
| port adapter | Port adapter binds the entry to the resolved delegation, includes the stored delegation and reason fields and the hash-input version in the chain input, and allocates each sequence number from the database in the same transaction as the entry. | Write two entries with the same body but different delegation IDs and check that the hashes differ; append two steps at the same time from two instances and check that sequence numbers have no gap or duplicate; strip the delegation or reason fields from a version 2 entry and check that verification fails. |
| HTTP handler | HTTP handler requires authentication, returns only entries of scopes the caller may read, refuses a cross-namespace read with audit.entry-not-found, answers 503 with Retry-After 5 when the database is unavailable, and the chain check names the first bad sequence number. | Request entries of another namespace with a namespace access token and check that the server denies with audit.entry-not-found; tamper one agent entry and check that GET /api/audit/check names its sequence number; block the database and check that reads and checks answer 503. |
