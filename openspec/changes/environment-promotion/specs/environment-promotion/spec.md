## ADDED Requirements

### Requirement: Compare unit config

When a caller requests a comparison of two units of one namespace, the server SHALL return the per-field difference of their effective config values.

#### Scenario: compareUnits shows staging and production difference
- **WHEN** caller requests comparison with source and target stable IDs in the query of one namespace
- **THEN** the server returns per-field source value, target value, equality state, and both stable IDs

### Requirement: Compare request shape

When a caller sends a comparison with source and target stable IDs in the query, the server SHALL validate the pair and refuse a bad pair.

#### Scenario: compareUnits refuses missing unit with 404
- **WHEN** a compare query names a missing or removed stable ID
- **THEN** the server returns 404 with error unit.not-found

#### Scenario: compareUnits refuses cross-tool pair with 422
- **WHEN** a compare query names units of different tools
- **THEN** the server returns 422 with error promotion.incompatible-tools

### Requirement: Compare needs target read

When a caller lacks unit:read on the source unit or on the target unit, the server SHALL refuse the comparison with 403.

#### Scenario: compareUnits refuses without target read
- **WHEN** a caller lacks unit:read on the target unit
- **THEN** the server returns 403 with no difference

### Requirement: Mask secrets in comparison

When a compared config field holds a secret value, the server SHALL return a masked marker and no plain text for that field.

#### Scenario: compareUnits masks secret values in diff
- **WHEN** a compared config field holds a secret value
- **THEN** the server returns a masked marker and no plain text for each secret field

### Requirement: Secret equality without plain text

When a compared config field holds a secret value on both units, the server SHALL return its equality state with no plain text.

#### Scenario: compareUnits shows secret equality state
- **WHEN** a compared config field holds a secret value on both units
- **THEN** the server returns equal or unequal for the secret field with no plain text

### Requirement: Mixed secret and plain

When a compared field is a secret on one unit and plain on the other, the server SHALL return unequal with no secret plain text.

#### Scenario: compareUnits marks mixed secret plain as unequal
- **WHEN** a field is a secret on one unit and plain on the other
- **THEN** the server returns unequal with masked secret side and no plain text

### Requirement: Refuse cross-tool promotion

If the source unit and the target unit use different tools, then the server SHALL refuse the promotion with 422.

#### Scenario: promoteUnits refuses Pulumi to OpenTofu copy
- **WHEN** caller requests promotion between units of different tools
- **THEN** the server refuses the request with 422 promotion.incompatible-tools

### Requirement: Refuse cross-blueprint promotion

If the source unit and the target unit come from different blueprints, then the server SHALL refuse the promotion with 422.

#### Scenario: promoteUnits refuses different blueprint copy
- **WHEN** caller requests promotion between units of different blueprints
- **THEN** the server refuses the request with 422 promotion.incompatible-blueprint

### Requirement: Promote config as edit

When a caller promotes selected config fields from a source unit to a target unit of the same tool, the server SHALL apply the copy as an edit of the target unit with a write-back commit and a plan.

#### Scenario: promoteUnits copies staging config to production with plan
- **WHEN** caller promotes selected config fields from source unit to target unit with unit:read on source and unit:manage on target
- **THEN** the server writes an edit of the target unit, starts a plan job, writes an audit entry, and evaluates gates

### Requirement: Promotion field allowlist

When a promotion field list names a field outside effective config values, the server SHALL refuse that field with 400.

#### Scenario: promoteUnits refuses identity field in promotion
- **WHEN** a promotion field list names an identity or system field
- **THEN** the server refuses the field with 400 promotion.field-not-allowed

### Requirement: Require explicit secret fields

When a promotion copies a secret field without an explicit secret flag, the server SHALL refuse the request with 400.

#### Scenario: promoteUnits refuses implicit secret copy
- **WHEN** a promotion omits the explicit secret flag for a secret field
- **THEN** the server refuses the request with 400 promotion.secret-requires-explicit-field

### Requirement: Keep secrets sealed in write-back

When a promotion copies a secret field with the explicit secret flag, the server SHALL store a sealed reference with no plain text in the write-back commit.

#### Scenario: promoteUnits stores secret as sealed reference
- **WHEN** a promotion copies a secret field with the explicit secret flag
- **THEN** the server writes a sealed reference and no plain text to the write-back commit

### Requirement: Refuse sealed scope mismatch

When the sealed reference of the source cannot seal for the target scope, the server SHALL refuse the promotion with 422.

#### Scenario: promoteUnits refuses unusable sealed reference
- **WHEN** the sealed reference of the source cannot seal for the target scope
- **THEN** the server refuses with 422 and copies no value

### Requirement: Refuse stale promotion

When the target unit changed after the compared revision, the server SHALL refuse the promotion with 409 and commit no edit.

#### Scenario: promoteUnits refuses promotion on concurrent target edit
- **WHEN** the promotion carries a target revision older than the current target revision
- **THEN** the server refuses with 409 unit.conflict and commits no edit

### Requirement: Reference units by stable ID

When a promotion names a unit by stable ID that has no live unit, the server SHALL return 404.

#### Scenario: promoteUnits refuses promotion for missing source
- **WHEN** the source stable ID names a missing or removed unit
- **THEN** the server returns 404 with error unit.not-found

#### Scenario: promoteUnits refuses promotion for missing target
- **WHEN** the target stable ID names a missing, removed, or other-namespace unit
- **THEN** the server returns 404 with error unit.not-found

#### Scenario: promoteUnits resolves renamed unit by stable ID
- **WHEN** a unit was renamed after the comparison
- **THEN** the server resolves the stable ID to the renamed live unit

### Requirement: Refuse promotion without target manage

When a caller lacks unit:manage on the target unit, the server SHALL refuse the promotion with 403.

#### Scenario: promoteUnits refuses read-only caller
- **WHEN** a caller holds unit:read on the source but lacks unit:manage on the target
- **THEN** the server returns 403 with no committed edit

#### Scenario: promoteUnits refuses without source read
- **WHEN** a caller lacks unit:read on the source unit
- **THEN** the server returns 403 with no committed edit

### Requirement: Refuse empty promotion

When a promotion names no config field, the server SHALL refuse the request with 400.

#### Scenario: promoteUnits refuses empty field list
- **WHEN** a promotion names no config field
- **THEN** the server refuses the request with 400 promotion.empty-fields

#### Scenario: promoteUnits reports no difference
- **WHEN** the selected fields hold equal values on both units
- **THEN** the server returns no-difference with no edit and no plan

### Requirement: Refuse locked target

When the target unit holds a manual lock or a running job, the server SHALL refuse the promotion and commit no edit.

#### Scenario: promoteUnits refuses locked target
- **WHEN** the target unit holds a manual lock or a running job
- **THEN** the server refuses with 423 and commits no edit

### Requirement: Keep committed edit on gate refusal

When a gate refuses the promotion plan, the server SHALL keep the committed target edit and return the decision record with 201.

#### Scenario: promoteUnits keeps edit on gate refusal
- **WHEN** a gate refuses the promotion plan
- **THEN** the server keeps the committed target edit with its write-back commit and returns 201 with the decision record

### Requirement: Keep committed edit when plan fails to start

When the plan job fails to start after the target edit committed, the server SHALL keep the committed edit and state that only a new edit undoes it.

#### Scenario: promoteUnits keeps edit when plan cannot start
- **WHEN** the plan job fails to start after the target edit committed
- **THEN** the server keeps the committed edit and returns an error that names a new edit as the undo path

### Requirement: Recover interrupted promotion

When the server restarts after the target edit committed but before the plan started, the server SHALL reuse the committed edit for a retry with a new plan.

#### Scenario: promoteUnits retries after crash between commit and plan
- **WHEN** the server restarts after the target edit committed but before the plan started
- **THEN** the server reuses the committed edit, starts a new plan, and writes an audit entry

### Requirement: Match retry by idempotency key

When a caller retries a promotion with the same Idempotency-Key inside 24 hours in one namespace, the server SHALL return the one promotion record and start at most 1 plan.

#### Scenario: promoteUnits matches retry by Idempotency-Key
- **WHEN** a caller retries a promotion with the same Idempotency-Key 1 hour after the first request
- **THEN** the server returns the one promotion ID with its committed edit and starts at most 1 plan
