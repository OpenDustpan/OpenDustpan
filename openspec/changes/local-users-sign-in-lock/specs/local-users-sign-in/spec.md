## ADDED Requirements

### Requirement: Lock notice ceiling

The server SHALL send at most 10 local password name-lock notices for each installation and fixed UTC clock hour by default.

#### Scenario: Flood across made-up names
- **WHEN** wrong passwords lock more than 10 names in one fixed UTC hour
- **THEN** the server sends at most 10 lock notices and one summary, and each name stays locked

#### Scenario: Hour boundary
- **WHEN** the UTC hour ends
- **THEN** the next lock in the new hour sends a normal notice

#### Scenario: Notice write fails
- **WHEN** the notice store write fails
- **THEN** the sign-in answers one 401 with the same text and the hourly count is unchanged

### Requirement: Summary notice

When locks pass the ceiling, the server SHALL write one summary notice with the count left out and the top sources.

#### Scenario: Read summary
- **WHEN** locks pass the ceiling in one hour
- **THEN** the summary holds the count left out and the grouped client addresses and no user name

#### Scenario: Summary audit
- **WHEN** the ceiling is passed
- **THEN** the audit log holds the summary write with the count left out

### Requirement: Policy shows ceiling

The server SHALL show the effective ceiling in GET /api/dustpan/v1/local-users/policy.

#### Scenario: Read policy
- **WHEN** a site administrator reads the policy with installation:manage-sign-in
- **THEN** the answer holds the ceiling and its effective value

#### Scenario: Policy refused
- **WHEN** a caller reads the policy without installation:manage-sign-in
- **THEN** the server denies the read

### Requirement: Ceiling setting

The server SHALL hold the ceiling as installation setting localuser.lock-notice-ceiling with default 10 and allowed values 1 to 100.

#### Scenario: Change ceiling
- **WHEN** a site administrator sets the ceiling to a value from 1 to 100
- **THEN** later hours use the new ceiling and the audit log holds the change

#### Scenario: Ceiling refused
- **WHEN** a caller sets the ceiling to 0 or above 100
- **THEN** the server denies the change with no effect

#### Scenario: Read ceiling in docs
- **WHEN** a site administrator reads the local users docs page
- **THEN** the docs page states 10 notices, the summary contents, and the 1 to 100 range
