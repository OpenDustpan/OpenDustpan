## ADDED Requirements

### Requirement: Flag default off

The server SHALL ship with the IP allowlist feature off. A scope that did not opt in SHALL have no list.

#### Scenario: Fresh installation
- **WHEN** the installation never turned the feature on
- **THEN** requests pass with no allowlist check

#### Scenario: Scope that did not opt in
- **WHEN** the feature is on and a namespace or an access token has no list
- **THEN** that scope adds no check

### Requirement: Deny when any list denies

When the client address sits outside any opted-in installation, namespace or access token list, the server SHALL answer 403 with the refusing scope. The answer SHALL NOT name a namespace or a token.

#### Scenario: Address misses one list
- **WHEN** the address matches one opted-in list but not another
- **THEN** the server answers 403 with error code access.ip-not-allowed and the scope

#### Scenario: Token list
- **WHEN** a valid access token that has a list is used from an address outside that list
- **THEN** the server answers 403 with error code access.ip-not-allowed and the scope `token`

### Requirement: Invalid entry

When a write of a list holds an invalid CIDR or address, more than 64 entries, or an empty list for a namespace or a token, the server SHALL answer 400 with error code ip-allowlist.invalid-cidr and store no change. A write that is longer than the stored value can hold SHALL answer 400 and name the limit.

#### Scenario: Bad CIDR
- **WHEN** a write holds 999.999.0.0/16
- **THEN** the server answers 400 and stores no change

#### Scenario: Too many entries
- **WHEN** a write holds 65 entries
- **THEN** the server answers 400 and stores no change

### Requirement: Check order

When the client address sits outside the opted-in installation list, the server SHALL answer 403 before credential validation. The token list and the namespace list SHALL be checked after authentication.

#### Scenario: Denied network
- **WHEN** the address is outside the installation list
- **THEN** the server answers 403 without checking the credential

### Requirement: Refresh of a console session

The server SHALL apply the allowlist check to the refresh of a console session. A refresh from an address outside an opted-in list SHALL NOT renew the session.

#### Scenario: Refresh from a denied address
- **WHEN** a request to the refresh route carries only the refresh cookie and the address is outside an opted-in list
- **THEN** the server answers 403 with error code access.ip-not-allowed and sets no new cookie

#### Scenario: Refresh from an allowed address
- **WHEN** the same request comes from an address inside each opted-in list
- **THEN** the server renews the session

### Requirement: Recorded address

The server SHALL check the recorded client address from the installation trusted-proxy handling.

#### Scenario: Spoofed header
- **WHEN** a request carries a proxy header from an untrusted source
- **THEN** the server ignores the header

### Requirement: Denial audit

When a request arrives from outside an opted-in list, the server SHALL write an audit entry with the scope and the address. Each change of a list SHALL write an audit entry.

#### Scenario: Denied request
- **WHEN** a token request arrives from a denied address
- **THEN** the server writes an audit entry

#### Scenario: Change of a list
- **WHEN** a person changes the list of a scope
- **THEN** the server writes an audit entry in the same transaction as the change

### Requirement: Empty list of the installation

When the feature is on and the installation list is empty, the server SHALL deny each address for the installation scope. A namespace or an access token with no list SHALL have no check.

#### Scenario: Empty installation list
- **WHEN** the feature is on and the installation list is empty
- **THEN** the server answers 403 for each address

#### Scenario: Removed namespace list
- **WHEN** the list of a namespace is removed
- **THEN** the namespace adds no check

### Requirement: List of an access token

The server SHALL store a list on an access token, read it in the lookup of the token, and apply it on each request that uses the token. A change of the list SHALL apply to the next request on each instance. Who may set the list, and the fresh step it needs, are set by open question 1 of the proposal.

#### Scenario: Token list applies at once
- **WHEN** the list of a token changes
- **THEN** the next request with that token uses the new list

#### Scenario: Stored list does not decode
- **WHEN** the stored list of a token does not decode
- **THEN** the server answers 403 for that token and writes a denial entry

### Requirement: List of a namespace

The server SHALL hold the list of a namespace as the setting `ipAllowlist.namespace` of the namespace settings resource, and SHALL show it with the view `/api/dustpan/v1/namespaces/{namespace}/settings/ip-allowlist`. The view SHALL add no rule. A change that widens or clears the list SHALL need a fresh step when the installation requires it. A change that narrows the list SHALL need none.

#### Scenario: Same change, two paths
- **WHEN** a person sets the key through the view and another person sets it through the namespace settings
- **THEN** both changes give the same revision, `ETag` and audit entry

#### Scenario: Widen
- **WHEN** a session without a fresh step widens the list
- **THEN** the server answers 403 with error code mfa.fresh-step-required and stores no change
