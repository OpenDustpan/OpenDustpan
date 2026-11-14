## ADDED Requirements

### Requirement: List and read ownership rules

The server SHALL answer list and read requests for the ownership rules of a scope.

#### Scenario: List pages rules of one scope
- **WHEN** The caller lists the ownership rules of a scope it can read.
- **THEN** The server answers the items of the scope with paging and the ETag of each rule.

#### Scenario: List without object read fails
- **WHEN** The caller holds namespace read but not the ownership-rule read action on the scope.
- **THEN** The server answers 403 and lists nothing.

#### Scenario: Bad page token fails
- **WHEN** The caller lists with an invalid page token.
- **THEN** The server answers 400 and changes nothing.

#### Scenario: Read names one rule
- **WHEN** The caller reads one ownership rule by ID.
- **THEN** The server answers the rule with its ETag.

### Requirement: Weakens definition

Where a caller writes an ownership rule, an approval rule or an approval default, the server SHALL treat the write as weakening when it lowers the approval burden.

#### Scenario: Mode relax weakens
- **WHEN** A write moves mode from enforce to evaluate.
- **THEN** The server treats the write as weakening.

#### Scenario: Count reduction weakens
- **WHEN** A write lowers count or removes a group or user.
- **THEN** The server treats the write as weakening.

#### Scenario: Added user tightens
- **WHEN** A write adds a group or a user with no other relax.
- **THEN** The server treats the write as non-weakening.

#### Scenario: Path widening weakens
- **WHEN** A write widens paths or lengthens freshAuthSeconds.
- **THEN** The server treats the write as weakening.

#### Scenario: Tighten does not weaken
- **WHEN** A write narrows paths, raises count or shortens freshAuthSeconds.
- **THEN** The server treats the write as non-weakening.

#### Scenario: Widened when weakens
- **WHEN** A write widens the when expression or the unless body so the rule covers fewer changes.
- **THEN** The server treats the write as weakening.

#### Scenario: Narrowed when tightens
- **WHEN** A write narrows the when expression or the unless body so the rule covers more changes.
- **THEN** The server treats the write as non-weakening.

#### Scenario: Delete removes a requirement
- **WHEN** A caller deletes an ownership rule.
- **THEN** The server treats the write as weakening.

### Requirement: Undecidable comparison fails closed

If the server cannot decide whether a when, unless or paths change widens coverage, then the server SHALL treat the write as weakening.

#### Scenario: Undecidable expression weakens
- **WHEN** The server cannot decide whether a when, unless or glob change widens coverage.
- **THEN** The server treats the write as weakening.

### Requirement: Create rule

When a caller creates an ownership rule, the server SHALL require If-None-Match star.

#### Scenario: Create with star saves
- **WHEN** The caller creates an ownership rule with If-None-Match star.
- **THEN** The server saves the rule and answers the rule with its new ETag.

#### Scenario: Create adds a requirement
- **WHEN** A caller creates a new ownership rule in evaluate mode.
- **THEN** The server treats the write as non-weakening.

#### Scenario: Duplicate create refused
- **WHEN** A caller creates an ID that already exists in the scope.
- **THEN** The server answers 409 and the stored rule is unchanged.

### Requirement: Put with precondition

When a caller updates an existing ownership rule, the server SHALL check If-Match against the stored ETag.

#### Scenario: Fresh match saves
- **WHEN** The caller puts an existing ownership rule with a fresh If-Match.
- **THEN** The server saves the rule and answers the rule with its new ETag.

#### Scenario: Stale match fails closed
- **WHEN** The caller puts an ownership rule with a stale If-Match.
- **THEN** The server answers 412 and the stored rule is unchanged.

#### Scenario: Missing match refused
- **WHEN** The caller updates or deletes an ownership rule with no If-Match.
- **THEN** The server answers 428 and the stored rule is unchanged.

#### Scenario: Hostile rule text refused
- **WHEN** A put carries a path traversal, an oversize glob set or a when expression above the evaluator limit.
- **THEN** The server answers 400 and the stored rule is unchanged.

#### Scenario: Malformed body refused
- **WHEN** A put carries an unknown field or a body over the size limit.
- **THEN** The server answers 400 and the stored rule is unchanged.

#### Scenario: ID mismatch refused
- **WHEN** A put carries a body ID different from the path ID.
- **THEN** The server answers 400 and the stored rule is unchanged.

#### Scenario: Narrower reuse refused
- **WHEN** A narrower scope saves a rule with the ID of a rule of a wider scope that holds it.
- **THEN** The server refuses the save and names the wider scope.

### Requirement: Delete with precondition

When a caller deletes an ownership rule, the server SHALL check If-Match against the stored ETag and answer 412 on a stale match.

#### Scenario: Delete removes one rule
- **WHEN** The caller deletes an ownership rule with a fresh If-Match.
- **THEN** The server deletes the rule and writes an audit entry.

#### Scenario: Stale delete fails closed
- **WHEN** The caller deletes an ownership rule with a stale If-Match.
- **THEN** The server answers 412 and the stored rule is unchanged.

#### Scenario: Unknown delete fails
- **WHEN** The caller deletes an unknown rule ID.
- **THEN** The server answers 404 and changes nothing.

#### Scenario: Delete racing put loses one side
- **WHEN** A delete and a put on one rule ID race.
- **THEN** The version compare lets one call win and the other answers 412 or 404 with no partial write.

### Requirement: Idempotent write

When a caller sends a mutating ownership-rule request with an Idempotency-Key, the server SHALL scope the key by principal, method and path.

#### Scenario: Same key replays same answer
- **WHEN** The caller retries a put with the same Idempotency-Key and the same body.
- **THEN** The server answers the stored answer and writes once.

#### Scenario: Same key with new body refused
- **WHEN** The caller sends the same Idempotency-Key with a different body.
- **THEN** The server answers 422 and the stored rule is unchanged.

#### Scenario: Same key from another caller is separate
- **WHEN** Another principal sends the same Idempotency-Key on the same route.
- **THEN** The server treats it as a new write.

### Requirement: Non-weakening write with access token

When a write does not weaken security, the server SHALL accept it with an access token.

#### Scenario: Tighten with access token
- **WHEN** A caller with an access token creates or tightens an ownership rule.
- **THEN** The server saves the rule.

### Requirement: Weakening write with access token

If a write weakens security, then the server SHALL refuse it with an access token.

#### Scenario: Weaken ownership rule with access token fails
- **WHEN** A caller with an access token weakens an ownership rule.
- **THEN** The server answers 403 with mfa.fresh-step-required and the rule is unchanged.

#### Scenario: Delete with access token fails
- **WHEN** A caller with only an access token deletes an ownership rule.
- **THEN** The server answers 403 with mfa.fresh-step-required and the rule is unchanged.

#### Scenario: Weaken approval rule with access token fails
- **WHEN** A caller with an access token weakens an approval rule or an approval default on its existing route.
- **THEN** The server answers 403 with mfa.fresh-step-required and the rule is unchanged.

#### Scenario: Fresh session weakens
- **WHEN** A person with a fresh authentication within the window weakens a rule.
- **THEN** The server saves the rule.

#### Scenario: Expired fresh window fails
- **WHEN** A person session weakens a rule after the fresh window expired.
- **THEN** The server answers 403 with mfa.fresh-step-required and the rule is unchanged.

#### Scenario: Token never gives fresh step
- **WHEN** A caller with only an access token repeats a weakening write after a sign-in.
- **THEN** The server still answers 403 with mfa.fresh-step-required.

#### Scenario: Agent weaken fails
- **WHEN** An agent or a service principal without a fresh step weakens an ownership rule.
- **THEN** The server answers 403 with mfa.fresh-step-required and the rule is unchanged.

#### Scenario: No factor fails
- **WHEN** A person with no enrolled factor tries a weakening write.
- **THEN** The server answers 403 with mfa.fresh-step-required and names the enrollment page.

#### Scenario: Destroy approval still needs fresh step
- **WHEN** A change holds a destroy.
- **THEN** The server still requires a fresh authentication for the approval.

### Requirement: Review mode guard

When a caller saves an ownership rule at a scope with review mode 4 below it, the server SHALL refuse the save.

#### Scenario: Mode 4 below refuses save
- **WHEN** A caller saves an ownership rule at a scope with review mode 4 below it.
- **THEN** The server refuses the save and names each scope with mode 4.

### Requirement: Audit of writes

When the server answers a put, delete or refused weakening write, the server SHALL write an audit entry.

#### Scenario: Put writes audit entry
- **WHEN** A put creates or updates an ownership rule.
- **THEN** The server writes an audit entry with principal, scope, rule ID and decision.

#### Scenario: Refused write writes audit entry
- **WHEN** A weakening write with an access token is refused.
- **THEN** The server writes an audit entry with the refusal code.
