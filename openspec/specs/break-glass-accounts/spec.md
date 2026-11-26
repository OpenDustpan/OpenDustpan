# break-glass-accounts Specification

## Purpose
TBD - created by archiving change keep-two-working-break-glass. Update Purpose after archive.

## Requirements

### Requirement: guard-last-enabled

If a disable or a delete leaves no enabled break-glass account, then the server SHALL refuse the change in its write statement and answer 409 with the code principal.no-working-break-glass.

#### Scenario: Disable of the last enabled account is refused
- **WHEN** a site administrator disables the last enabled break-glass account
- **THEN** the server answers 409 with principal.no-working-break-glass, keeps the account enabled and writes an audit entry for the refused change

#### Scenario: Delete of the last enabled account is refused
- **WHEN** a site administrator deletes the last enabled break-glass account
- **THEN** the server answers 409 with principal.no-working-break-glass and keeps the account

#### Scenario: Disable that leaves one enabled account passes
- **WHEN** a site administrator disables one of two enabled break-glass accounts
- **THEN** the server accepts the change, writes an audit entry and names the live count

#### Scenario: Disable in maintenance mode
- **WHEN** a site administrator disables one of two accounts in maintenance mode
- **THEN** the server accepts the change

### Requirement: sweep-exempt

The inactivity sweep SHALL NOT disable a break-glass account, and SHALL write one audit record for each break-glass account that it skips.

#### Scenario: Sweep skips an inactive break-glass account
- **WHEN** the sweep runs and a break-glass account is inactive for longer than the period
- **THEN** the account stays enabled and the sweep writes one record of kind change with outcome success, in scope @server, with the principal reference of the account as object and the reason "break-glass account is exempt"

#### Scenario: Sweep with no break-glass account
- **WHEN** the sweep runs and no break-glass account is inactive
- **THEN** the sweep writes no exemption record

### Requirement: add-raises-count

When a site administrator adds a break-glass account with a fresh authentication, the server SHALL accept the add whatever the current count, and SHALL store the account, its first passkey and the audit entry in one transaction.

#### Scenario: Add at zero accounts
- **WHEN** no working account exists and an administrator adds one with a passkey
- **THEN** the server accepts the add

#### Scenario: Add stores account and first passkey together
- **WHEN** an administrator adds a break-glass account with a passkey
- **THEN** the server stores the account, its first passkey and the audit entry in one transaction

#### Scenario: Failed passkey insert leaves no account
- **WHEN** an administrator adds a break-glass account and the insert of its first passkey fails
- **THEN** the transaction rolls back, the server stores no account and no audit entry, and the answer carries the error of the passkey insert

#### Scenario: Add without fresh step
- **WHEN** the add comes with a stale fresh step
- **THEN** the server answers 403 with mfa.fresh-step-required

### Requirement: report-count

When a site administrator reads the list of break-glass accounts, the server SHALL compute the live count of working paths at read time.

#### Scenario: Account disabled before the read
- **WHEN** an account is disabled and an administrator reads the list
- **THEN** the count excludes the account and the missing count rises

#### Scenario: Upgrade reports missing accounts
- **WHEN** an existing installation holds fewer than two working accounts
- **THEN** the list route reports the missing count and an administrator adds accounts without a new setup

### Requirement: guard-race

If two requests disable the last two enabled break-glass accounts together, then the server SHALL accept one request and refuse the other.

#### Scenario: Second request after the first commit
- **WHEN** two requests disable the last two accounts
- **THEN** one request succeeds and the other gets 409 with principal.no-working-break-glass

#### Scenario: Sign-in during a disable
- **WHEN** a break-glass sign-in runs while a disable runs
- **THEN** the sign-in takes no guard step and writes a whole session

### Requirement: restore-guard

If a restore leaves fewer than two working break-glass paths in the staged data of a scope, then the server SHALL end the restore as failed in the swap transaction, with the code principal.no-working-break-glass, and keep the live data.

#### Scenario: Old backup with one account
- **WHEN** an operator restores a backup with one working account
- **THEN** the start route still answers 202, the restore ends failed with the code principal.no-working-break-glass, and the data stays unchanged

#### Scenario: Backup with two accounts
- **WHEN** an operator restores a backup with two working accounts
- **THEN** the server runs the swap

#### Scenario: Swap fails half way
- **WHEN** the swap fails after the check
- **THEN** the transaction rolls back and the live data stays

#### Scenario: Count of one namespace
- **WHEN** the NEW count `CountWorkingBreakGlassInNamespace` runs for a namespace and the break-glass principals of that namespace are fewer than two, while another namespace has more
- **THEN** the count gives only the principals of the given namespace and the restore check refuses

### Requirement: fresh-step

If a guarded change of a break-glass account comes without a fresh authentication, then the server SHALL answer 403 before it runs the change.

#### Scenario: Stale step on rotation
- **WHEN** a site administrator rotates a credential with a stale fresh step
- **THEN** the server answers 403 with mfa.fresh-step-required and changes nothing

#### Scenario: Person with no factor
- **WHEN** a person with no enrolled factor starts a rotation
- **THEN** the server answers 403 with mfa.fresh-step-required and the text names `/api/dustpan/v1/me/passkeys/register/begin`

### Requirement: atomic-rotation

When a site administrator rotates the credential of a break-glass account, the server SHALL insert the new credential before it ends the old credential in one transaction.

#### Scenario: Rotation keeps one working credential
- **WHEN** a site administrator rotates the credential
- **THEN** the new credential signs in and no commit leaves the account without a working credential

#### Scenario: Failed insert keeps the old credential
- **WHEN** the insert of the new credential fails
- **THEN** the transaction rolls back and the old credential stays active

#### Scenario: Unknown or other account
- **WHEN** the id is unknown or names an account that is not a break-glass account
- **THEN** the server answers 404 with break-glass.not-found

#### Scenario: Parallel rotations
- **WHEN** two rotations of the same account run together
- **THEN** the second waits and then rotates the new credential
