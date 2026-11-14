## ADDED Requirements

### Requirement: reduced-envelope

When the e2e suite checks a reduced deployment, the server SHALL expose an export envelope that the e2e asserts for version 3 and the root custom field.

#### Scenario: ReducedWithFeatures
- **WHEN** the reduced document holds features
- **THEN** e2e asserts version 3, features preserved, and the sole root resource holds custom field false

#### Scenario: ReducedWithoutFeatures
- **WHEN** the reduced document holds no features
- **THEN** e2e asserts version 3 with no features key and the sole root resource holding custom field false

### Requirement: job-matrix

When job tokens come from real runner-harness claims, the server SHALL let the e2e matrix check reduced documents and sharing-rule allow and deny behavior.

#### Scenario: ReducedDocumentAndAllowDenyMatrix
- **WHEN** job tokens come from real runner-harness claims
- **THEN** TestOutputSharingRules passes without skips on SQLite and on Linux with PostgreSQL across every harness-listed version

#### Scenario: OperatorPositiveControl
- **WHEN** the operator control runs against the outputs route with an access token
- **THEN** the operator with a sharing rule reads cross-unit outputs and without a rule the read is denied

### Requirement: empty-secret-guard

When token creation returns success with an empty or missing secret, the server SHALL let e2e helpers fail before use without changing intentional administrator calls.

#### Scenario: EmptyAndMissingSecretGuard
- **WHEN** the injected issuer returns success with an empty or missing secret
- **THEN** outputReader and checkOperatorSavesNoCrossRule return an error before any request

#### Scenario: AdminCallsUnchanged
- **WHEN** helpers run with valid secrets
- **THEN** intentional administrator calls still send their nonempty credential
