## ADDED Requirements

### Requirement: Database clock expiry

When the server reads a lease, session, or sign-in expiry through IsExpired, the server SHALL decide expiry through IsExpired against the database clock read in the same statement.

#### Scenario: Skewed process clock through IsExpired
- **WHEN** one instance with a process clock 60 s ahead calls IsExpired on a lease, session, or sign-in expiry
- **THEN** both instances report the same live or expired answer from IsExpired

### Requirement: Clock gap fails closed

When the database clock read for IsExpired fails, the server SHALL answer 503 with Retry-After 5 in the shape of the API.

#### Scenario: Gap during IsExpired read
- **WHEN** the database clock read for IsExpired fails
- **THEN** the server writes nothing and answers 503 with Retry-After 5

### Requirement: Shared failure budget

When a sign-in fails, the server SHALL record the failure once in the shared database windowed counter.

#### Scenario: Failures from two instances
- **WHEN** failed sign-in attempts arrive through different instances
- **THEN** the budget counts each failure once and refuses sign-in past the stored window

#### Scenario: Success writes nothing
- **WHEN** a sign-in succeeds
- **THEN** the budget row stays unchanged
