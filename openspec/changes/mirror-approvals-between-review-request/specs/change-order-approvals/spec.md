## ADDED Requirements

### Requirement: Mirror forge approval to pending review

When a forge approve event arrives for a linked principal, the server SHALL record a pending review for that principal.

#### Scenario: Forge approve becomes pending review
- **WHEN** a forge approve event arrives for a linked principal
- **THEN** the change order holds one pending review for the linked principal with one audit entry that names the forge source and no signature yet

### Requirement: Pending review becomes signature

When the linked principal completes a fresh OpenDustpan step inside the shortest window that holds, the server SHALL record a signature for the pending review.

#### Scenario: Pending review with fresh proof signs
- **WHEN** the linked principal completes a fresh OpenDustpan step inside the shortest window that holds
- **THEN** the change order holds one signature for the linked principal

### Requirement: Mirror-in stale proof refused

If a pending forge approval waits with a missing or stale fresh proof, then the server SHALL refuse the signature.

#### Scenario: Stale mirrored approval refused
- **WHEN** a pending forge approval waits with a missing or stale fresh proof
- **THEN** the server answers 403 with error code mfa.fresh-step-required and records no signature

#### Scenario: Window 0 asks every time on mirror-in
- **WHEN** the window is 0 and a pending forge approval waits
- **THEN** the server answers 403 with error code mfa.fresh-step-required unless a fresh step of age zero is present

### Requirement: Rule-level fresh window

When an approval rule sets freshAuthSeconds and the approval arrives outside that rule window, the server SHALL refuse the approval.

#### Scenario: Rule window refuses stale approval
- **WHEN** an approval rule sets freshAuthSeconds and the approval arrives outside that rule window
- **THEN** the server answers 403 with error code mfa.fresh-step-required and records no signature

### Requirement: Link ownership at point of use

When a forge identity has no stored OAuth link owned by that person at the point of signature write, the server SHALL record no signature.

#### Scenario: Unlinked forge user ignored
- **WHEN** a forge approve event arrives for a forge user with no stored OAuth link owned by that person
- **THEN** the server records no pending review and no signature and writes one audit entry that names the unlinked identity

#### Scenario: Revoked link blocks signature
- **WHEN** the OAuth link is revoked or changed between the forge event and the signature write
- **THEN** the server records no signature

#### Scenario: API approval checks link at use
- **WHEN** a principal without an owned OAuth link calls approve through the API on a version-control unit
- **THEN** the server answers 403 with a reason that names the missing OAuth link and records no signature

### Requirement: Replay and supersede

When the same forge event ID arrives twice, the server SHALL keep one signature for that event.

#### Scenario: Replayed event keeps one signature
- **WHEN** the same forge approve event ID arrives twice, including two concurrent deliveries
- **THEN** the change order still holds one signature for the event ID

#### Scenario: Later review supersedes earlier
- **WHEN** a request-changes event arrives after an approve event for the same principal and unit
- **THEN** the change order holds the later verdict for that principal

### Requirement: Post OpenDustpan approval to forge

When a linked principal approves on the change order inside the fresh window, the server SHALL post the approval to the forge as the review of that person.

#### Scenario: OpenDustpan approve posts review
- **WHEN** a linked principal approves on the change order inside the fresh window
- **THEN** the review request shows the review of the person and the change order keeps one signature

### Requirement: Mirror request-changes

When a forge request-changes event arrives for a linked principal with a fresh OpenDustpan proof inside the window, the server SHALL record a request-changes thread.

#### Scenario: Forge request-changes opens thread
- **WHEN** a forge request-changes event arrives for a linked principal
- **THEN** the change order holds one request-changes thread for the linked principal after the fresh check

#### Scenario: OpenDustpan request-changes posts
- **WHEN** a linked principal requests changes on the change order inside the fresh window
- **THEN** the review request shows the request-changes review of the person

### Requirement: Forge post failure

When the forge post of an approval fails, the server SHALL keep the signature and retry with backoff up to three times.

#### Scenario: Failed forge post keeps signature and retries
- **WHEN** the forge post of an approval fails
- **THEN** the signature stays, the server retries with backoff up to three times, then writes one dead-letter audit entry that names the failed direction while the change order stays the source of truth

### Requirement: Refuse without OAuth link

If a principal without an OAuth link approves a version-control unit, then the server SHALL refuse with a reason that names the missing OAuth link.

#### Scenario: Approve without OAuth refused
- **WHEN** a principal without an OAuth link calls approve or request-changes on a version-control unit
- **THEN** the server answers 403 with a reason that names the missing OAuth link and records no signature

### Requirement: No enrolled factor refused

When a linked OAuth principal with no enrolled factor approves, the server SHALL refuse with error code mfa.fresh-step-required.

#### Scenario: OAuth person with no factor refused
- **WHEN** a linked OAuth principal with no enrolled factor approves
- **THEN** the server answers 403 with error code mfa.fresh-step-required that names the enrollment page

### Requirement: Namespace switch for all approvals

When the namespace switch for approvals is on, the server SHALL refuse a non-destroy approval outside the fresh window.

#### Scenario: Switch on refuses stale approval
- **WHEN** the namespace switch is on and a non-destroy approval arrives outside the window
- **THEN** the server answers 403 with error code mfa.fresh-step-required

### Requirement: Unlink never retracts past signature

When a forge user unlinks after a mirrored signature, the server SHALL keep the past signature.

#### Scenario: Unlink keeps past signature
- **WHEN** a forge user unlinks after a mirrored signature
- **THEN** the past signature stays and only a new explicit retract removes it
