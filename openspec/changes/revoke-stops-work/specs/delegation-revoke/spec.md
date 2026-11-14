## ADDED Requirements

### Requirement: revoked-delegation-blocks-next-request

When the delegation is revoked, the server SHALL deny the next agent request under that delegation.

#### Scenario: request-after-revoke
- **WHEN** The agent sends any request under a revoked delegation.
- **THEN** The server answers 403 with the NEW error code delegation.revoked and writes one denied audit entry that names the agent, the delegation, and the reason revoked.

#### Scenario: prior-answer-replay
- **WHEN** The agent replays the bytes of a prior allow answer after revocation.
- **THEN** The server denies the request with the NEW error code delegation.revoked.

#### Scenario: store-error-fails-closed
- **WHEN** The delegation store returns an error during authorization.
- **THEN** The server denies the request and writes one denied audit entry with reason store-unavailable.

### Requirement: no-scheduled-work-starts

When the delegation is revoked, the server SHALL refuse to start scheduled work of that delegation.

#### Scenario: scheduler-after-revoke
- **WHEN** The scheduler evaluates scheduled work of a revoked delegation.
- **THEN** No turn, plan job, or scheduled step of the delegation starts, and each refused start writes one denied audit entry collapsed to one entry per delegation per minute.

#### Scenario: queued-work-after-revoke
- **WHEN** Revocation commits while runs of the delegation wait in queue.
- **THEN** Each queued run ends with the NEW stop reason revoked and no queued step writes.

### Requirement: running-work-stops

When the person revokes the delegation during live work, the server SHALL end each live run of that delegation.

#### Scenario: revoke-during-run
- **WHEN** The person revokes the delegation while runs of the delegation have live work.
- **THEN** Each live run of the delegation ends with the NEW stop reason revoked, each live durable claim is cancelled at the next write boundary, and no step writes after that boundary.

#### Scenario: takeover-after-revoke
- **WHEN** A second instance takes over a lease of a revoked delegation after a crash.
- **THEN** The taking instance reads revoked time, ends the run with the NEW stop reason revoked, and writes nothing.

### Requirement: in-flight-work-freezes

When revocation lands with in-flight work, the server SHALL keep finished results and freeze each proposed change without an apply under the revoked delegation.

#### Scenario: finished-call-stands
- **WHEN** The agent already finished a tool call and proposed a change order before revocation.
- **THEN** The finished tool result stands, the change order stays proposed, it still needs a person confirmation, and no apply step runs under the revoked delegation.
