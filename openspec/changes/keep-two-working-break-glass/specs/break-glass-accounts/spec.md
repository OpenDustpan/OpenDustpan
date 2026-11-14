## ADDED Requirements

### Requirement: guard-last-enabled

If a disable, delete or sweep leaves no enabled break-glass account, then the server SHALL refuse the change in its write transaction.

#### Scenario: Disable of the last enabled account is refused
- **WHEN** a site administrator disables the last enabled break-glass account
- **THEN** the server answers 409, keeps the account enabled and writes an audit entry for the refused change

#### Scenario: Disable that leaves one enabled account passes
- **WHEN** a site administrator disables one of two enabled break-glass accounts
- **THEN** the server accepts the change, writes an audit entry, notifies the other administrators and names the live count

#### Scenario: Disable in maintenance mode
- **WHEN** a site administrator disables one of two accounts in maintenance mode
- **THEN** the server accepts the change

#### Scenario: Sweep of the last account is refused
- **WHEN** the sweep tries to delete the last enabled break-glass account
- **THEN** the server refuses it and writes a record that names the sweep, the account and the reason

### Requirement: guard-floor

If a grant revoke, a credential change, a passkey removal, an expiry change, a policy block or an IP allowlist change leaves fewer than two working break-glass paths, then the server SHALL refuse the change in its write transaction.

#### Scenario: IP allowlist change below the floor is refused
- **WHEN** an IP allowlist change leaves one working path
- **THEN** the server answers 409 with the live count, keeps the old list and writes an audit entry

#### Scenario: Passkey removal below the floor is refused
- **WHEN** a person removes the passkey of a break-glass account and the count would fall below two
- **THEN** the server answers 409 and keeps the passkey

#### Scenario: Policy block below the floor is refused
- **WHEN** a policy block blocks an account and the count would fall below two
- **THEN** the server answers 409 and keeps the account unblocked

#### Scenario: Expiry change below the floor is refused
- **WHEN** an expiry change puts an account in the past and the count would fall below two
- **THEN** the server answers 409 and keeps the old expiry

#### Scenario: Hosted admin grant revoke is refused
- **WHEN** a revoke of the role admin of a namespace leaves fewer than two break-glass principals
- **THEN** the server answers 409 and keeps the grant

#### Scenario: A change that does not lower the count passes
- **WHEN** the installation holds one working path and a change leaves the count the same
- **THEN** the server accepts the change

### Requirement: add-raises-count

When a site administrator adds a break-glass account with a fresh authentication, the server SHALL accept the add whatever the current count.

#### Scenario: Add at zero accounts
- **WHEN** no working account exists and an administrator adds one with a passkey
- **THEN** the server accepts the add

#### Scenario: Add without fresh step
- **WHEN** the add comes with a stale fresh step
- **THEN** the server answers 403 with mfa.fresh-step-required

### Requirement: report-count

When a site administrator reads the list of break-glass accounts, the server SHALL compute the live count of working paths at read time.

#### Scenario: Account expires by time
- **WHEN** an account expires with no write and an administrator reads the list
- **THEN** the count excludes the account and the missing count rises

### Requirement: guard-race

If a concurrent write holds the lock on the break-glass set past the lock timeout, then the server SHALL answer 409 with the code break-glass.busy and write nothing.

#### Scenario: Second request after the first commit
- **WHEN** two requests disable the last two accounts
- **THEN** one request succeeds and the other gets 409

#### Scenario: Lock timeout
- **WHEN** the lock wait passes the timeout
- **THEN** the server answers 409 with break-glass.busy, stores no change and allows a safe retry

#### Scenario: Sign-in during a lock wait
- **WHEN** a break-glass sign-in runs while a guarded change waits for the lock
- **THEN** the sign-in does not take the guard lock and writes a whole session

### Requirement: restore-guard

If a restore leaves fewer than two working break-glass paths in the staged data of a scope, then the server SHALL refuse the restore before the swap.

#### Scenario: Old backup with one account
- **WHEN** an operator restores a backup with one working account
- **THEN** the server answers 409 with break-glass.restore-refused and the data stays unchanged

#### Scenario: Backup with two accounts
- **WHEN** an operator restores a backup with two working accounts
- **THEN** the server runs the swap

#### Scenario: Swap fails half way
- **WHEN** the swap fails after the check
- **THEN** the transaction rolls back and the live data stays

#### Scenario: Hosted namespace restore
- **WHEN** a restore brings a namespace whose break-glass principals sit in another namespace
- **THEN** the server counts only the principals of the restored namespace and refuses when they are fewer than two

### Requirement: fresh-step

If a guarded change of a break-glass account comes without a fresh authentication, then the server SHALL answer 403 before it runs the change.

#### Scenario: Stale step on rotation
- **WHEN** a site administrator rotates a credential with a stale fresh step
- **THEN** the server answers 403 with mfa.fresh-step-required and changes nothing

#### Scenario: Person with no factor
- **WHEN** a person with no enrolled factor starts a rotation
- **THEN** the server answers 403 and the text names the page that enrolls a factor

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

### Requirement: setup-needs-two

When first setup finishes, the server SHALL refuse completion until two break-glass accounts with a passkey or a hardware key exist.

#### Scenario: Finish with one account is refused
- **WHEN** first setup finishes with one account
- **THEN** the server answers 409 and names the missing count

#### Scenario: Hardware key counts
- **WHEN** first setup finishes with two accounts that each hold a hardware key
- **THEN** the server completes the setup

#### Scenario: Upgrade reports missing accounts
- **WHEN** an existing installation holds fewer than two working accounts
- **THEN** the list route reports the missing count and an administrator adds accounts without a new setup
