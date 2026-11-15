## ADDED Requirements

### Requirement: enroll-first-totp

When a person with no factor presents a valid unused 128-bit enrollment code inside its one-hour life or a one-time first-enrollment-only email code with a valid TOTP setup, the server SHALL store one TOTP factor for the person.

#### Scenario: Enroll with code then list
- **WHEN** a person with no factor presents a valid unused enrollment code with a valid TOTP setup
- **THEN** the server stores one TOTP factor for the person and writes one audit entry

#### Scenario: Reuse or expiry fails
- **WHEN** the enrollment code is expired or already used
- **THEN** the server answers 403, stores no factor and writes one audit entry

### Requirement: enroll-first-passkey

When a person with no factor presents a valid unused 128-bit enrollment code inside its one-hour life or a one-time first-enrollment-only email code with a valid attestation, the server SHALL store one passkey credential for the person.

#### Scenario: Enroll with code
- **WHEN** a person with no factor presents a valid unused enrollment code with a valid attestation
- **THEN** the server stores one passkey credential and writes one audit entry

#### Scenario: Email code second use fails
- **WHEN** the same email code arrives a second time
- **THEN** the server answers 403 and stores no credential

### Requirement: issue-enrollment-code

When an administrator with server:manage-sign-in requests an enrollment code for a person, the server SHALL issue one 128-bit single-use code with a one-hour life.

#### Scenario: Administrator issues code
- **WHEN** an administrator with server:manage-sign-in requests an enrollment code for a person
- **THEN** the server shows the code one time, stores only its hash with a one-hour expiry and writes one audit entry

### Requirement: concurrent-enrollment

When two enroll requests with the same enrollment code arrive together, the server SHALL store one factor from the first winner.

#### Scenario: Concurrent use keeps one
- **WHEN** two enroll requests with the same enrollment code arrive together
- **THEN** the server stores one factor, rejects the second with 403 and writes one audit entry for each attempt

### Requirement: factor-change-needs-fresh-step

When a person with a factor adds or removes a factor with no fresh step inside the window, the server SHALL refuse the change.

#### Scenario: Remove needs fresh step
- **WHEN** a person with a factor calls delete with no fresh step inside the window
- **THEN** the server answers 403 with error code mfa.fresh-step-required and removes no factor

### Requirement: step-up-restarts-window

When a person with an enrolled factor passes a TOTP, passkey or SSH-signature check inside the sign-in, the server SHALL restart the freshness window of that sign-in.

#### Scenario: Fresh step in one sign-in
- **WHEN** a person passes a TOTP check inside the sign-in
- **THEN** the server records the fresh step in the sealed access cookie of that sign-in and treats a later operation inside the window as fresh

#### Scenario: Other sign-in fails
- **WHEN** a fresh step recorded in one sign-in arrives with another sign-in
- **THEN** the server answers 403 with error code mfa.fresh-step-required

### Requirement: refuse-without-factor

While MFA is on, the server SHALL refuse a fresh-step operation of a plain OAuth or trusted-header person with no enrolled factor.

#### Scenario: Refuse token creation
- **WHEN** MFA is on and a person with no enrolled factor requests token creation
- **THEN** the server answers 403 with error code mfa.fresh-step-required

### Requirement: refusal-shape

When the server refuses a fresh-step operation for a plain OAuth or trusted-header person, the server SHALL name the factor step when the person has a factor and the enrollment page when the person has none.

#### Scenario: Name step or enrollment
- **WHEN** the server refuses a fresh-step operation
- **THEN** the 403 names the factor step when the person has a factor and the enrollment page when the person has none

### Requirement: factor-change-notice

When a person enrolls or removes a factor, the server SHALL write one audit entry and add 1 to the session generation of the person.

#### Scenario: Remove writes entry
- **WHEN** a person removes a factor
- **THEN** the server writes one audit entry and adds 1 to the session generation

#### Scenario: Notice fails
- **WHEN** no notification channel exists or delivery fails
- **THEN** the server still writes the audit entry and adds 1 to the session generation

### Requirement: email-code-never-fresh

When a person presents an email enrollment code at step-up, the server SHALL refuse it as a fresh step.

#### Scenario: Email code refused
- **WHEN** a person presents an email enrollment code at step-up
- **THEN** the server answers 403 with error code mfa.fresh-step-required

### Requirement: ssh-signature-fresh-step

When a person presents a valid SSH signature with an enrolled SSH key, the server SHALL accept the signature as a fresh step.

#### Scenario: SSH fallback passes
- **WHEN** a person presents a valid SSH signature with an enrolled SSH key
- **THEN** the server accepts the signature as a fresh step

#### Scenario: Invalid SSH fails
- **WHEN** the signature is invalid, replayed or from a revoked key
- **THEN** the server answers 403 with error code mfa.fresh-step-required and records no step

### Requirement: single-use-challenge

When the same TOTP code or WebAuthn assertion arrives twice, the server SHALL reject the second use.

#### Scenario: Replay fails
- **WHEN** the same code arrives twice, concurrently or after a restart
- **THEN** the server rejects the second use with no state change

### Requirement: rate-limit-attempts

When TOTP or enrollment-code attempts pass the per-person retry limit, the server SHALL refuse further attempts until the limit resets.

#### Scenario: Brute force slows
- **WHEN** attempts pass the per-person retry limit
- **THEN** the server answers 429 and writes one audit entry

### Requirement: require-second-factor-setting

Where the installation setting require-second-factor-for-plain-oauth is on, the server SHALL require a fresh app-factor step for each fresh-step operation of a plain OAuth session.

#### Scenario: Setting on
- **WHEN** the setting is on
- **THEN** the server refuses each fresh-step operation of a plain OAuth session until the person passes an app-factor step

#### Scenario: Setting off
- **WHEN** the setting is off
- **THEN** the server applies only the default freshness rule

### Requirement: restore-leaves-factors-unreadable

When a restore or a lost key-encryption key leaves factor secrets undecryptable, the server SHALL refuse each fresh-step check until re-enrollment.

#### Scenario: Restore into new installation
- **WHEN** factor secrets are undecryptable after a restore
- **THEN** the server answers 403 with error code mfa.fresh-step-required for each fresh-step check and requires re-enrollment
