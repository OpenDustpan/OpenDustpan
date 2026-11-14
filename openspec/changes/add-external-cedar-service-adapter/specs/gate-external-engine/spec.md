## ADDED Requirements

### Requirement: Evaluate through the service

When the installation names an external Cedar service, the server SHALL evaluate each unit through that service.

#### Scenario: Service evaluates a decision point
- **WHEN** the installation names an external Cedar service and a change reaches a decision point
- **THEN** the server returns the matched policy IDs and diagnostics for each unit.

### Requirement: Endpoint allowlist holds

When the service endpoint is not https or resolves to a denied address, the server SHALL give gate.ErrUnavailable.

#### Scenario: Internal redirect target
- **WHEN** the endpoint redirects or resolves to a private, loopback, or link-local address
- **THEN** the evaluation gives gate.ErrUnavailable.

### Requirement: Transport guards hold

When the service endpoint, TLS CA, credential, timeout, or answer size check fails, the server SHALL give gate.ErrUnavailable.

#### Scenario: Wrong CA or missing credential
- **WHEN** TLS uses a wrong CA or the credential is missing
- **THEN** the evaluation gives gate.ErrUnavailable.

#### Scenario: Slow or oversize service
- **WHEN** the service exceeds the 5 s timeout or the 1 MB answer limit
- **THEN** the evaluation gives gate.ErrUnavailable.

### Requirement: Bad answer is unavailable

If an answer does not authenticate, does not echo both IDs, or names an unknown or extra policy ID, then the server SHALL give gate.ErrUnavailable.

#### Scenario: Answer without echo
- **WHEN** an answer does not authenticate, does not echo request ID and policy-set version, or names an unknown or extra policy ID
- **THEN** the evaluation gives gate.ErrUnavailable.

### Requirement: Cedar diagnostic fails closed

When the service returns a Cedar diagnostic intact, the server SHALL stop the change in enforce mode with gate.ErrRuleFailed.

#### Scenario: Diagnostic in enforce mode
- **WHEN** the service returns a Cedar diagnostic intact for a gate in enforce mode
- **THEN** the change stops with gate.ErrRuleFailed and the record holds result error with the Cedar message.

#### Scenario: Diagnostic in evaluate mode
- **WHEN** the service returns a Cedar diagnostic intact for a gate in evaluate mode
- **THEN** the record holds the diagnostic with no effect on the change.

### Requirement: Outage enforce fails closed

While the service has an outage, the server SHALL fail closed with gate.ErrUnavailable for each enforce-mode gate.

#### Scenario: Outage at a gated decision point
- **WHEN** the service has an outage and the decision point has a gate in enforce mode
- **THEN** the server stops the change with gate.ErrUnavailable.

#### Scenario: Outage with evaluate gates only
- **WHEN** the service has an outage and the decision point has gates in evaluate mode only
- **THEN** the server records the miss with no stop.

### Requirement: Outage keeps access-limit calls deciding

While the service has an outage, the server SHALL keep access-limit checks on the linked adapter.

#### Scenario: Outage with access-limit-only call
- **WHEN** the service has an outage and a call needs only an access-limit check
- **THEN** the server decides the call on the linked adapter.

### Requirement: Save refuses on outage

When the service does not answer the check operation at save, the server SHALL refuse the save with gate.ErrUnavailable.

#### Scenario: Save during a service outage
- **WHEN** the service does not answer the check operation at save
- **THEN** the save is refused with gate.ErrUnavailable.

### Requirement: Version mismatch is unavailable

When the echoed policy-set version disagrees with the sent version, the server SHALL give gate.ErrUnavailable.

#### Scenario: Service echoes a new version
- **WHEN** the echoed policy-set version disagrees with the sent gate ID and gate version
- **THEN** the evaluation gives gate.ErrUnavailable and never uses a cached set.

### Requirement: Credential change needs restart

When the operator changes the service address, CA, or credential, the server SHALL apply the value only after a restart.

#### Scenario: Address or credential change
- **WHEN** the operator changes the service address, CA, or credential
- **THEN** the server uses the new value only after a restart and keeps the sealed credential unread in logs.

### Requirement: Save miss is recorded

When a save is refused because the service did not answer, the server SHALL record the fallback name and the miss.

#### Scenario: Refused save records the miss
- **WHEN** a save is refused because the service did not answer
- **THEN** the decision record holds result error with the fallback name and the service miss.

### Requirement: Break-glass keeps gates

When a break-glass session reaches a decision point during a service outage, the server SHALL meet the gates per mode.

#### Scenario: Break-glass during outage
- **WHEN** a break-glass session reaches a decision point during a service outage
- **THEN** the change stops per enforce mode and the record holds the miss.

### Requirement: Server starts with unhealthy engine

When the service is unreachable at boot, the server SHALL start and mark the engine unhealthy.

#### Scenario: Service unreachable at boot
- **WHEN** the service is unreachable at boot
- **THEN** the server starts, marks the engine unhealthy, and fails gated calls closed per mode.

#### Scenario: Cached set during outage
- **WHEN** the cache holds compiled sets from before the outage
- **THEN** the server never evaluates from the cached set and gives gate.ErrUnavailable for enforce gates.
