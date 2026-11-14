## ADDED Requirements

### Requirement: Preview claims

When the caller holds profile:read on the profile, the server SHALL answer the claims and the subject that a token of the named block and context would carry.

#### Scenario: Preview answers
- **WHEN** a caller with profile:read posts a profile, a block and a context
- **THEN** the server answers 200 with the claims and the subject

#### Scenario: Preview refused
- **WHEN** a caller without profile:read posts a preview
- **THEN** the server answers 403 and signs nothing

### Requirement: Preview signs nothing

When the caller previews claims, the server SHALL sign no token and call no adapter.

#### Scenario: No signer
- **WHEN** a caller posts a valid preview
- **THEN** no key is used and no adapter is called

### Requirement: Preview validation

If the preview request names an unknown profile, block or context value, then the server SHALL refuse with profile.invalid.

#### Scenario: Unknown value
- **WHEN** a caller posts an unknown block
- **THEN** the server answers 422 with profile.invalid

#### Scenario: Cross namespace
- **WHEN** a caller posts a profile of another namespace
- **THEN** the server answers 404

### Requirement: Preview matches settings

When the caller previews claims, the server SHALL apply the subject template, allowBlockSubjects and includeActorName of the namespace.

#### Scenario: Template and actor
- **WHEN** a caller posts a valid preview
- **THEN** the server answers the subject of the namespace template with the caller as actor

### Requirement: Preview absent facts

When the caller previews claims, the server SHALL omit each fact that the server does not know at preview time.

#### Scenario: Absent facts
- **WHEN** a caller posts a valid preview
- **THEN** time and token fields are absent and no value is invented

### Requirement: Preview refusals

If the preview subject or audience fails the issue checks, then the server SHALL refuse with issuer.invalid.

#### Scenario: Too long
- **WHEN** the subject of the preview exceeds the subject limit
- **THEN** the server answers 422 with issuer.invalid

### Requirement: Preview no audit

When the caller previews claims, the server SHALL write no audit entry.

#### Scenario: No record
- **WHEN** a caller posts a valid preview
- **THEN** no audit entry is written
