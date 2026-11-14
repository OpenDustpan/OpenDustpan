## ADDED Requirements

### Requirement: Feature flag default

While the installation feature flag is off, the server SHALL apply no IP allowlist check.

#### Scenario: Flag off passes
- **WHEN** the installation feature flag is off
- **THEN** the server applies no IP allowlist check and writes no denial entry

### Requirement: Flag default off

The server SHALL ship with the installation feature flag off.

#### Scenario: Fresh installation has no check
- **WHEN** the installation never turned the feature on
- **THEN** console sign-in and token requests pass the allowlist stage

### Requirement: Enable privilege

When a site administrator turns the feature on, the server SHALL accept the change.

#### Scenario: Site administrator enables
- **WHEN** a site administrator turns the feature on
- **THEN** the server accepts the change and writes an audit entry

### Requirement: Invalid list entry

When a settings write holds an entry that is not a valid IPv4 or IPv6 CIDR or address, the server SHALL answer 400.

#### Scenario: Bad CIDR refused
- **WHEN** a settings write holds an entry that is not a valid IPv4 or IPv6 CIDR or address
- **THEN** the server answers 400 with error code ip-allowlist.invalid-cidr and stores no change

### Requirement: Config audit

When a site administrator turns the feature on or changes any list, the server SHALL write an audit entry.

#### Scenario: List change audited
- **WHEN** a site administrator turns the feature on or changes any list
- **THEN** the server writes an audit entry with the actor, the scope, and the new list

### Requirement: Scope without list

While a scope did not opt in, the server SHALL apply no IP allowlist to that scope.

#### Scenario: Token without list passes
- **WHEN** the feature flag is on and the token, its namespace and the installation hold no list
- **THEN** the request continues to authentication and authorization

### Requirement: Deny when any list denies

When the client address sits outside any opted-in installation, namespace, or token list, the server SHALL answer 403 with the refusing scope in the body.

#### Scenario: Address misses one list
- **WHEN** the client address matches one opted-in list but sits outside another opted-in installation, namespace, or token list
- **THEN** the server answers 403 with error code ip-allowlist.denied that names the installation, namespace, or token scope that refused

### Requirement: Allowed request

When the client address matches each opted-in list, the server SHALL continue the request.

#### Scenario: Address on each list
- **WHEN** the client address matches each opted-in installation, namespace and token list
- **THEN** the request continues to authentication and authorization

### Requirement: Recorded client address

The server SHALL check the recorded client address from the installation trusted-proxy handling.

#### Scenario: Spoofed header ignored
- **WHEN** a request carries a proxy header from an untrusted source
- **THEN** the server checks the recorded client address and ignores the untrusted header

### Requirement: Check order

When the client address sits outside the opted-in installation list, the server SHALL answer 403 before credential validation.

#### Scenario: Denied network learns nothing
- **WHEN** the client address sits outside the opted-in installation list
- **THEN** the server answers 403 without checking the credential

### Requirement: Per-scope order

When the address passes the installation list but sits outside an opted-in namespace or token list, the server SHALL answer 403 after authentication.

#### Scenario: Namespace and token lists check after sign-in proof
- **WHEN** the address passes the installation list but sits outside an opted-in namespace or token list
- **THEN** the server answers 403 without reading state

### Requirement: Denial audit

When a console sign-in or a request with an access token arrives from outside an opted-in list, the server SHALL write an audit entry with the scope and the address.

#### Scenario: Denied request audited
- **WHEN** a console sign-in or a request with an access token arrives from outside an opted-in list
- **THEN** the server writes an audit entry with the scope that refused and the client address, and with the refused identity when known

### Requirement: List change timing

When an administrator changes a list, the server SHALL check each later request against the new list.

#### Scenario: Next request uses new list
- **WHEN** an administrator changes a list
- **THEN** the server checks the next request against the new list and leaves in-flight requests on the prior verdict

### Requirement: Replica lists

When an administrator changes a list on one instance, the server SHALL check each later request on each instance against the new list.

#### Scenario: Changed list reaches each replica
- **WHEN** an administrator changes a list on one instance
- **THEN** a request on another replica after the change uses the new list

### Requirement: Startup parse failure

When a stored list fails to parse at startup, the server SHALL fail closed for console sign-in and access token requests.

#### Scenario: Bad stored list fails closed
- **WHEN** a stored list fails to parse at startup
- **THEN** the server refuses console sign-in and access token requests until an operator repairs the list through the host database path

### Requirement: Check scope

When a console sign-in starts or a request carries an access token, the server SHALL check the client address.

#### Scenario: Token request checked
- **WHEN** a request with an access token reaches a tool-neutral, Pulumi, TFE-compatible, or settings route
- **THEN** the server checks the client address against each opted-in list

### Requirement: Self-lockout warning

When a settings write stores a list that excludes the client address of the writer, the server SHALL include a self-lockout warning in the answer.

#### Scenario: Own address excluded
- **WHEN** a settings write stores a list that excludes the client address of the writer
- **THEN** the answer includes a warning that names the self-lockout risk
