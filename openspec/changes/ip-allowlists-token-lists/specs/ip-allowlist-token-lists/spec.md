## ADDED Requirements

### Requirement: List of an access token

The server SHALL store a list on an access token, read it in the lookup of the token, and apply it on each request that uses the token. A change of the list SHALL apply to the next request on each instance. The list SHALL hold at most 64 entries and at most 4096 bytes. Who may set the list, and the fresh step it needs, are set by open question 1 of the proposal.

#### Scenario: Token list applies at once
- **WHEN** the list of a token changes
- **THEN** the next request with that token uses the new list

#### Scenario: Stored list does not decode
- **WHEN** the stored list of a token does not decode
- **THEN** the server answers 403 for that token and writes a denial entry

#### Scenario: Token list
- **WHEN** a valid access token that has a list is used from an address outside that list
- **THEN** the server answers 403 with error code access.ip-not-allowed and the scope `token`, and the answer does not name the token

#### Scenario: Token with no list
- **WHEN** the feature is on and an access token has no list
- **THEN** the token list adds no check

#### Scenario: Bad write
- **WHEN** a write of a token list holds an invalid entry, an empty list, more than 64 entries or more than 4096 bytes
- **THEN** the server answers 400 and stores no change

#### Scenario: A token sets a list
- **WHEN** a request that uses an access token sets a token list
- **THEN** the server answers 403 and stores no change

### Requirement: Token list in the check order

The server SHALL check the token list after authentication, and SHALL read it in the same lookup that finds the token. A request with a token that fails authentication SHALL read no token list.

#### Scenario: Failed authentication
- **WHEN** a request carries a token that fails authentication
- **THEN** the server reads no token list
