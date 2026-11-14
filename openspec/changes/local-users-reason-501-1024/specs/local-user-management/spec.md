## ADDED Requirements

### Requirement: Overlong reason refused

When a local-user create or unlock carries a reason above 500 Unicode code points, the server SHALL refuse it with 400 and the NEW error code `local-user.reason-too-long`.

#### Scenario: Create with 501 runes refused
- **WHEN** a site administrator calls CreateLocalUser with a reason of 501 runes or more
- **THEN** the server answers 400 with code local-user.reason-too-long that names the 500-rune limit, and CreateLocalUser writes no user and no audit entry

#### Scenario: Unlock with 501 runes refused
- **WHEN** a site administrator calls UnlockLocalUser with a reason of 501 runes or more
- **THEN** the server answers 400 with code local-user.reason-too-long, and UnlockLocalUser changes no state

#### Scenario: Reason of 1025 runes refused with limit code
- **WHEN** a site administrator calls CreateLocalUser with a reason of 1025 runes
- **THEN** the server answers 400 with code local-user.reason-too-long, not reason-required

### Requirement: Boundary and missing reason

When a local-user create or unlock carries a reason of 500 Unicode code points or fewer, the server SHALL accept the length check and continue with the existing required-reason check.

#### Scenario: Reason of 500 runes accepted
- **WHEN** a site administrator sends a reason of exactly 500 Unicode code points
- **THEN** the server processes the create or unlock as before

#### Scenario: Multibyte reason of 500 runes accepted
- **WHEN** a site administrator sends a reason of exactly 500 multibyte Unicode code points
- **THEN** the server passes the length check and continues as before

#### Scenario: Missing reason still required
- **WHEN** a site administrator sends a create or unlock with no reason
- **THEN** the server answers 400 with code local-user.reason-required

### Requirement: Check order and unchanged causes

While handling a local-user create or unlock, the server SHALL evaluate authorization before reason presence and reason length, and keep all other failure codes unchanged.

#### Scenario: Unauthorized caller with overlong reason denied first
- **WHEN** a caller without the local-user grant calls CreateLocalUser with a reason above 500 runes
- **THEN** the server answers 401 or 403 and not 400 reason-too-long

#### Scenario: Store outage on unlock unchanged
- **WHEN** a site administrator calls UnlockLocalUser during a store outage with a reason within the limit
- **THEN** the server answers the existing unavailable answer and not reason-too-long
