## ADDED Requirements

### Requirement: List of an access token

The server SHALL store a list on an access token, read it in the lookup of the token, and apply it on each request that uses the token. A change of the list SHALL apply to the next request on each instance. The list SHALL hold at most 64 entries and at most 4096 bytes. Who may set the list is set by the requirement "Who sets the list of an access token".

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


### Requirement: Token list in the check order

The server SHALL check the token list after authentication, and SHALL read it in the same lookup that finds the token. A request with a token that fails authentication SHALL read no token list.

#### Scenario: Failed authentication
- **WHEN** a request carries a token that fails authentication
- **THEN** the server reads no token list

### Requirement: Who sets the list of an access token

The server SHALL let the maker of a personal token set and clear its list, and SHALL let a holder of `principal:manage-tokens` on the service principal of a service token set and clear its list. This is the check of a revoke. The server SHALL ask for no fresh step. The server SHALL refuse every other caller, a holder of `server:manage-tokens` on a personal token included, and SHALL store no change for a refused caller. Each change SHALL write an audit entry.

#### Scenario: Maker sets own personal token list
- **WHEN** the person who made a personal token sets its list with a valid list
- **THEN** the server stores the list, writes the audit entry `token:set-ip-allowlist`, and shows the list as `ipAllowlist` in the token resource

#### Scenario: Token-management holder sets a service token list
- **WHEN** a holder of `principal:manage-tokens` on the service principal of a service token sets its list
- **THEN** the server stores the list and writes the audit entry `token:set-ip-allowlist`

#### Scenario: Clear of a list
- **WHEN** an allowed caller clears the list of a token
- **THEN** the server removes the list, writes the audit entry `token:remove-ip-allowlist`, and shows `ipAllowlist` as `[]`

#### Scenario: Person without the action is refused
- **WHEN** a person who is not the maker of a personal token, or who holds no `principal:manage-tokens` on the service principal of a service token, sets or clears the list
- **THEN** the server answers 404 on the own route or 403 on the service route, and stores no change

#### Scenario: Administrator of other personal tokens is refused
- **WHEN** a holder of `server:manage-tokens` who did not make a personal token sets its list
- **THEN** the server answers 404 on the own route, and stores no change

### Requirement: A token never widens its own list

The server SHALL refuse a change of a token list when the request is made with that same token and the change widens the list. A token with no list admits every address, so any list narrows it and a clear changes nothing. A token with a list is widened by a clear and by a list that admits an address that the stored list refuses. The server SHALL always accept a narrowing. The service and the store SHALL each make this check, and the store SHALL make it in the transaction of the write.

#### Scenario: Request made with the token cannot widen its own list
- **WHEN** a request made with a token that has the list `203.0.113.0/24` sets the list of that token to `203.0.113.0/24,198.51.100.0/24`
- **THEN** the server answers 403 with error code ip-allowlist.widen-refused, and the stored list stays `203.0.113.0/24`

#### Scenario: Request made with the token cannot clear its own list
- **WHEN** a request made with a token that has a list clears the list of that token
- **THEN** the server answers 403 with error code ip-allowlist.widen-refused, and the stored list stays

#### Scenario: Request made with the token may narrow its own list
- **WHEN** a request made with a token that has the list `203.0.113.0/24` sets the list of that token to `203.0.113.128/25`
- **THEN** the server stores the narrower list and writes the audit entry `token:set-ip-allowlist`

#### Scenario: Token with no list may set a list on itself
- **WHEN** a request made with a token that has no list sets a valid list on that token
- **THEN** the server stores the list

#### Scenario: Console session may widen the list
- **WHEN** the maker uses a console session, not the token, and widens the list of the token
- **THEN** the server stores the wider list

#### Scenario: Race between two changes
- **WHEN** two requests made with the same token change its list at the same time
- **THEN** each request is compared with the list that the store holds in the transaction of its own write, and a widening is refused whatever the order
