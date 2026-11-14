## ADDED Requirements

### Requirement: Compare unit config

When a caller requests a comparison of two units of one namespace, the server SHALL return the per-field difference of their effective config values.

#### Scenario: Show staging and production difference
- **WHEN** caller requests comparison of staging and production units by stable ID
- **THEN** the server returns per-field source value, target value, equality state, and both stable IDs

### Requirement: Mask secrets in comparison

When a compared config field holds a secret value, the server SHALL return a masked marker and no plain text for that field.

#### Scenario: Mask secret values in diff
- **WHEN** a compared config field holds a secret value
- **THEN** the server returns a masked marker and no plain text for each secret field

### Requirement: Secret equality without plain text

When a compared config field holds a secret value on both units, the server SHALL return its equality state with no plain text.

#### Scenario: Show secret equality state
- **WHEN** a compared config field holds a secret value on both units
- **THEN** the server returns equal or unequal for the secret field with no plain text

### Requirement: Refuse cross-tool promotion

If the source unit and the target unit use different tools, then the server SHALL refuse the promotion.

#### Scenario: Refuse Pulumi to OpenTofu copy
- **WHEN** caller requests promotion between units of different tools
- **THEN** the server refuses the request with error promotion.incompatible-tools

### Requirement: Refuse cross-blueprint promotion

If the source unit and the target unit come from different blueprints, then the server SHALL refuse the promotion.

#### Scenario: Refuse different blueprint copy
- **WHEN** caller requests promotion between units of different blueprints
- **THEN** the server refuses the request with error promotion.incompatible-blueprint

### Requirement: Promote config as edit

When a caller promotes selected config fields from a source unit to a target unit of the same tool, the server SHALL apply the copy as an edit of the target unit with a write-back commit and a plan.

#### Scenario: Copy staging config to production with plan
- **WHEN** caller promotes selected config fields from source unit to target unit with unit:read on source and unit:manage on target
- **THEN** the server writes an edit of the target unit, starts a plan job, writes an audit entry, and evaluates gates

### Requirement: Promotion field allowlist

When a promotion field list names a field outside effective config values, the server SHALL refuse that field.

#### Scenario: Refuse identity field in promotion
- **WHEN** a promotion field list names an identity or system field
- **THEN** the server refuses the field with error promotion.field-not-allowed

### Requirement: Require explicit secret fields

When a promotion copies a secret field without an explicit secret flag, the server SHALL refuse the request.

#### Scenario: Refuse implicit secret copy
- **WHEN** a promotion omits the explicit secret flag for a secret field
- **THEN** the server refuses the request with error promotion.secret-requires-explicit-field

### Requirement: Keep secrets sealed in write-back

When a promotion copies a secret field with the explicit secret flag, the server SHALL store a sealed reference with no plain text in the write-back commit.

#### Scenario: Store secret as sealed reference
- **WHEN** a promotion copies a secret field with the explicit secret flag
- **THEN** the server writes a sealed reference and no plain text to the write-back commit

### Requirement: Refuse stale promotion

When the target unit changed after the compared revision, the server SHALL refuse the promotion with 409 and commit no edit.

#### Scenario: Refuse promotion on concurrent target edit
- **WHEN** the promotion carries a target revision older than the current target revision
- **THEN** the server refuses with 409 unit.conflict and commits no edit

### Requirement: Reference units by stable ID

When a promotion names a unit by stable ID that has no live unit, the server SHALL return 404.

#### Scenario: Refuse promotion for missing source
- **WHEN** the source stable ID names a missing or removed unit
- **THEN** the server returns 404 with error unit.not-found

#### Scenario: Refuse promotion for missing target
- **WHEN** the target stable ID names a missing, removed, or other-namespace unit
- **THEN** the server returns 404 with error unit.not-found

#### Scenario: Resolve renamed unit by stable ID
- **WHEN** a unit was renamed after the comparison
- **THEN** the server resolves the stable ID to the renamed live unit

### Requirement: Refuse promotion without target manage

When a caller lacks unit:manage on the target unit, the server SHALL refuse the promotion with 403.

#### Scenario: Refuse read-only caller
- **WHEN** a caller holds unit:read on the source but lacks unit:manage on the target
- **THEN** the server returns 403 with no committed edit

### Requirement: Refuse empty promotion

When a promotion names no config field, the server SHALL refuse the request.

#### Scenario: Refuse empty field list
- **WHEN** a promotion names no config field
- **THEN** the server refuses the request with error promotion.empty-fields

#### Scenario: Report no difference
- **WHEN** the selected fields hold equal values on both units
- **THEN** the server returns no-difference with no edit and no plan

### Requirement: Keep committed edit on gate refusal

When a gate refuses the promotion plan, the server SHALL keep the committed target edit and return the decision record.

#### Scenario: Gate refuses promotion plan
- **WHEN** a gate refuses the promotion plan
- **THEN** the server keeps the committed target edit with its write-back commit and returns 201 with the decision record

### Requirement: Keep committed edit when plan fails to start

When the plan job fails to start after the target edit committed, the server SHALL keep the committed edit and state that only a new edit undoes it.

#### Scenario: Plan fails to start after commit
- **WHEN** the plan job fails to start after the target edit committed
- **THEN** the server keeps the committed edit and returns an error that names a new edit as the undo path

### Requirement: Recover interrupted promotion

When the server restarts after the target edit committed but before the plan started, the server SHALL reuse the committed edit for a retry with a new plan.

#### Scenario: Retry after crash between commit and plan
- **WHEN** the server restarts after the target edit committed but before the plan started
- **THEN** the server reuses the committed edit, starts a new plan, and writes an audit entry
