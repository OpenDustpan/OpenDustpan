## ADDED Requirements

### Requirement: floor-on-grant-revoke

If a revoke of an access grant lowers the count of working break-glass accounts and leaves fewer than two, then the server SHALL refuse the revoke in its write transaction and answer 409 with the code principal.no-working-break-glass.

#### Scenario: Revoke that leaves one working account is refused
- **WHEN** a site administrator revokes the grant that makes one of two working break-glass accounts work
- **THEN** the server answers 409 with principal.no-working-break-glass, keeps the grant and writes an audit entry for the refused change

#### Scenario: Revoke that leaves two working accounts passes
- **WHEN** a site administrator revokes the grant of one of three working break-glass accounts
- **THEN** the server accepts the revoke and writes an audit entry

#### Scenario: Revoke below the floor that does not lower the count
- **WHEN** one working account exists and a site administrator revokes a grant that does not make a break-glass account work
- **THEN** the server accepts the revoke

#### Scenario: Revoke of the role admin on the hosted service
- **WHEN** an administrator of a namespace revokes the role admin of one of two break-glass principals of that namespace
- **THEN** the server answers 409 with principal.no-working-break-glass and the count of another namespace is unchanged

### Requirement: floor-on-passkey-removal

If the removal of a passkey lowers the count of working break-glass accounts and leaves fewer than two, then the server SHALL refuse the removal in its write transaction and answer 409 with the code principal.no-working-break-glass.

#### Scenario: Removal of the last passkey of a break-glass account is refused
- **WHEN** a person removes the only passkey of a break-glass account and only one other working account exists
- **THEN** the server answers 409 with principal.no-working-break-glass and keeps the passkey

#### Scenario: Removal of a passkey of another person
- **WHEN** a person removes a passkey of an account that is not a break-glass account
- **THEN** the server accepts the removal and does not count

#### Scenario: Two parallel removals
- **WHEN** two administrators remove the last passkeys of two break-glass accounts in parallel and three working accounts exist
- **THEN** at most one removal is stored when it would leave fewer than two, and the other one answers 409

### Requirement: floor-on-ip-allowlist-write

If the write of an IP allowlist lowers the count of working break-glass accounts and leaves fewer than two, then the server SHALL refuse the write in its write transaction and answer 409 with the code principal.no-working-break-glass.

#### Scenario: List write that lowers the count below two is refused
- **WHEN** a site administrator writes a list and the count after the write is below two and lower than before
- **THEN** the server answers 409 with principal.no-working-break-glass and keeps the earlier list

#### Scenario: List write that does not lower the count
- **WHEN** a site administrator writes a list on a self-hosted installation
- **THEN** the server accepts the write

### Requirement: floor-refusal-audited

Each refusal of a guarded write SHALL write an audit entry in the same transaction as the check, and SHALL change no row of the write.

#### Scenario: Refusal writes one audit entry
- **WHEN** the server refuses a revoke, a passkey removal or a list write
- **THEN** the audit log holds one entry with the action of the route and the object of the write, and the grant, the passkey or the list is unchanged

#### Scenario: Failed audit write
- **WHEN** the audit write of a guarded write fails
- **THEN** the server stores no change of the write
