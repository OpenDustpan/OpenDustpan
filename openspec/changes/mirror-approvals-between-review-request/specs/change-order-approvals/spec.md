## ADDED Requirements

### Requirement: Pending review from forge approval

When a forge approve event arrives for a linked principal, the server SHALL record a pending review for that principal.

#### Scenario: Forge approve becomes pending review
- **WHEN** a forge approve event arrives for a linked principal (recordPendingReview)
- **THEN** the change order holds one pending review with one audit entry that names the forge source and no signature yet

### Requirement: Signature after fresh step

When the linked principal completes a fresh OpenDustpan step inside the shortest window that holds, the server SHALL record a signature for the pending review.

#### Scenario: Pending review with fresh proof signs
- **WHEN** the linked principal posts an approval with a fresh proof inside the window (recordMirroredSignature)
- **THEN** the change order holds one signature for the principal

### Requirement: Stale proof refused

If a pending forge approval has a missing or stale fresh proof, then the server SHALL refuse the signature.

#### Scenario: Stale mirrored approval refused
- **WHEN** a pending forge approval waits with a stale fresh proof
- **THEN** the server answers 403 with error code mfa.fresh-step-required and records no signature

#### Scenario: Window 0 asks every time
- **WHEN** the window is 0 and no fresh step of age zero is present
- **THEN** the server answers 403 with error code mfa.fresh-step-required

### Requirement: Rule-level window

When an approval arrives outside the freshAuthSeconds window of its approval rule, the server SHALL refuse the approval.

#### Scenario: Rule window refuses stale approval
- **WHEN** a rule sets freshAuthSeconds and the approval arrives outside it
- **THEN** the server answers 403 with error code mfa.fresh-step-required and records no signature

#### Scenario: Shortest window wins
- **WHEN** installation, namespace and rule windows differ
- **THEN** the server uses the shortest of the three

### Requirement: Link ownership at point of use

When a forge identity has no stored OAuth link owned by that person at the point of signature write, the server SHALL record no signature.

#### Scenario: Unlinked forge user ignored
- **WHEN** a forge approve event arrives for a forge user with no link
- **THEN** the server records no pending review and no signature and writes one audit entry

#### Scenario: Revoked link blocks signature
- **WHEN** the link is revoked between the event and the signature write
- **THEN** the server records no signature

### Requirement: Stable forge user ID

When a forge event arrives, the server SHALL key the OAuth link on the stable forge user ID.

#### Scenario: Renamed login keeps link
- **WHEN** a forge login name changes and the user ID stays
- **THEN** the event maps to the same principal

### Requirement: Replay idempotency

When the same forge event ID arrives twice, the server SHALL keep one signature for that event.

#### Scenario: Replayed event keeps one signature
- **WHEN** the same event ID arrives twice, including two concurrent deliveries
- **THEN** the change order holds one signature for the event ID

### Requirement: Supersede by later review

When a later forge review event arrives for the same principal and unit, the server SHALL mark the earlier signature superseded.

#### Scenario: Later review supersedes earlier
- **WHEN** a request-changes event with a later event time follows an approve event
- **THEN** the earlier signature is superseded and the later verdict holds

### Requirement: Post to forge

When a linked principal approves on the change order inside the fresh window, the server SHALL post the review to the forge as the review of that person.

#### Scenario: OpenDustpan approve posts review
- **WHEN** a linked principal approves on the change order (postReviewToForge)
- **THEN** the review request shows the review of the person

### Requirement: Mirror request-changes

When a forge request-changes event arrives for a linked principal with a fresh proof inside the window, the server SHALL record a request-changes thread.

#### Scenario: Forge request-changes opens thread
- **WHEN** a forge request-changes event arrives for a linked principal
- **THEN** the change order holds one request-changes thread after the fresh check

#### Scenario: OpenDustpan request-changes posts
- **WHEN** a linked principal requests changes on the change order
- **THEN** the review request shows the request-changes review

### Requirement: Forge post failure

When the forge post of an approval fails, the server SHALL retry the post up to 3 times with backoff and keep the signature.

#### Scenario: Failed post retries then dead letter
- **WHEN** the forge post fails on each attempt (postReviewToForge)
- **THEN** the signature stays and one dead-letter audit entry names the failed direction

#### Scenario: Stale head never posts
- **WHEN** the forge head commit moved after the review event
- **THEN** the server marks the pending review stale and records no signature

### Requirement: Refuse without OAuth link

If a principal without an OAuth link approves a version-control unit, then the server SHALL refuse with a reason that names the missing OAuth link.

#### Scenario: Approve without OAuth refused
- **WHEN** a principal without a link calls approve or request-changes
- **THEN** the server answers 403 with a reason that names the missing link and records no signature

#### Scenario: Dismiss without OAuth refused
- **WHEN** a principal without a link calls dismiss
- **THEN** the server answers 403 with a reason that names the missing link

### Requirement: No enrolled factor

When a linked OAuth principal with no enrolled factor approves, the server SHALL refuse with error code mfa.fresh-step-required.

#### Scenario: OAuth person with no factor refused
- **WHEN** a linked principal with no enrolled factor approves
- **THEN** the server answers 403 with error code mfa.fresh-step-required that names the enrollment page

### Requirement: Agent and token never fresh

When an agent principal or an access-token caller approves a change that needs a fresh step, the server SHALL refuse.

#### Scenario: Agent approval refused
- **WHEN** an agent approves a change that needs a fresh step
- **THEN** the server answers 403 with error code mfa.fresh-step-required

### Requirement: Namespace switch

While the namespace switch freshAuthForApprovals is on, the server SHALL refuse a non-destroy approval outside the fresh window.

#### Scenario: Switch on refuses stale approval
- **WHEN** the switch is on and an approval arrives outside the window
- **THEN** the server answers 403 with error code mfa.fresh-step-required

### Requirement: Pending review life cycle

When a pending review waits with no fresh step, the server SHALL expire it after 24 hours.

#### Scenario: Pending review expires and cancels
- **WHEN** a pending review waits with no fresh step
- **THEN** the server lists it, accepts a cancel, and expires it after 24 hours

#### Scenario: Closed order drops pending review
- **WHEN** the change order closes before the fresh step
- **THEN** the server records no signature

### Requirement: Unlink keeps signature

When a forge user unlinks after a mirrored signature, the server SHALL keep the past signature.

#### Scenario: Unlink keeps past signature
- **WHEN** a forge user unlinks after a mirrored signature
- **THEN** the past signature stays and only a new explicit retract removes it
