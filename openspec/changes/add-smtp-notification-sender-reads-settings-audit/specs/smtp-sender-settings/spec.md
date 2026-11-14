## ADDED Requirements

### Requirement: smtp-settings-visible

Where SMTP settings exist, the server SHALL answer each SMTP relay, sender, and plain-connection key with its effective value and its source.

#### Scenario: Read shows relay sender and opt-in with source
- **WHEN** operator reads the installation settings
- **THEN** answer lists each SMTP key with its effective value and its source layer through ReadEffective and never includes the password value

### Requirement: smtp-password-source

When the operator sets the SMTP password, the server SHALL accept it only from the NEW allowlist OPENDUSTPAN_SMTP_PASSWORD or the file that NEW OPENDUSTPAN_SMTP_PASSWORD_FILE names (PatchSettings) and refuse a database value.

#### Scenario: PATCH of password is refused
- **WHEN** operator PATCHes the SMTP password database value
- **THEN** server answers 409 with settings.patch-invalid and changes nothing

### Requirement: smtp-password-precedence

When both named password sources are set, the server SHALL use the NEW OPENDUSTPAN_SMTP_PASSWORD_FILE file over NEW OPENDUSTPAN_SMTP_PASSWORD, and a missing or unreadable or empty file means unset.

#### Scenario: Both named sources set
- **WHEN** both NEW OPENDUSTPAN_SMTP_PASSWORD and NEW OPENDUSTPAN_SMTP_PASSWORD_FILE hold a password
- **THEN** effective source is the NEW OPENDUSTPAN_SMTP_PASSWORD_FILE file and GET source names file (ReadEffective)

### Requirement: smtp-patch-atomic

When the operator PATCHes installation settings with an SMTP password key, the server SHALL refuse the whole PATCH and change nothing.

#### Scenario: Mixed PATCH changes nothing
- **WHEN** operator PATCHes SMTP password together with a valid relay key
- **THEN** server answers 409 and no key changes and revision does not move (PatchSettings)

#### Scenario: Null deletes database value
- **WHEN** operator PATCHes a database SMTP key to null
- **THEN** database value is deleted and effective value returns to default and revision moves

### Requirement: smtp-optin-fail-closed

If the plain-SMTP opt-in value is missing or not valid, then the server SHALL treat the effective value as off.

#### Scenario: Invalid opt-in value is off
- **WHEN** the plain opt-in value is missing or not a boolean
- **THEN** effective value is off and a dial refuses and names the address, the setting, and the fix call

### Requirement: smtp-optin-audited

When the operator changes the plain-SMTP opt-in, the server SHALL write an audit entry with principal, old value, and new value.

#### Scenario: Opt-in change writes audit
- **WHEN** operator changes the plain-SMTP opt-in
- **THEN** audit entry records principal, old value, new value, and source (PatchSettings)

### Requirement: hosted-plain-refused

Where the installation is the hosted service, the server SHALL refuse the plain-SMTP opt-in and keep the effective value off.

#### Scenario: Hosted service refuses opt-in
- **WHEN** operator enables plain SMTP on the hosted service
- **THEN** change is refused with settings.locked and the effective value stays off (PatchSettings)

### Requirement: smtp-hosted-refusal-audited

When the hosted service refuses the plain-SMTP opt-in, the server SHALL write an audit entry with the principal and the refused value.

#### Scenario: Refused hosted attempt writes audit
- **WHEN** operator attempts the plain opt-in on the hosted service
- **THEN** audit entry records principal, refused value, and hosted marker

### Requirement: smtp-optin-fresh-step

When the operator turns the plain-SMTP opt-in from off to on, the server SHALL require a fresh authentication.

#### Scenario: Opt-in on needs fresh step
- **WHEN** operator turns the plain opt-in from off to on without a fresh authentication
- **THEN** server answers 403 with mfa.fresh-step-required and changes nothing (PatchSettings)

### Requirement: smtp-connection-check

When the site administrator runs the NEW SMTP connection check, the server SHALL report relay reachability without sending a message and write an audit entry.

#### Scenario: Check reports relay reachability
- **WHEN** site administrator runs the SMTP connection check
- **THEN** answer reports reachable or refused with the address, the setting to change, and the fix call, and writes an audit entry (CheckSmtpConnection)

### Requirement: smtp-check-bounds

When the site administrator runs the NEW SMTP connection check, the server SHALL dial only the configured relay with a 10 s timeout and at most 1 check in flight per installation.

#### Scenario: Check uses configured relay only
- **WHEN** site administrator runs the SMTP connection check
- **THEN** check dials only the configured relay with a 10 s timeout and follows the TLS rules (CheckSmtpConnection)
