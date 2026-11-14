## ADDED Requirements

### Requirement: Paging scope

Where a list route pages under /api/dustpan/v1, the server SHALL accept pageSize and pageToken.

#### Scenario: Tool-neutral scope
- **WHEN** the caller pages a list
- **THEN** the server changes only routes under /api/dustpan/v1

#### Scenario: Tool routes unchanged
- **WHEN** the caller pages a Pulumi or TFE route
- **THEN** the server keeps Pulumi and TFE paging behavior unchanged

### Requirement: Deprecated paging aliases

When a caller sends limit or cursor on an in-scope list route, the server SHALL treat them as pageSize and pageToken.

#### Scenario: Old names still work
- **WHEN** the caller sends limit and cursor on an in-scope list route
- **THEN** the server answers the same page as with pageSize and pageToken

#### Scenario: Conflict prefers new names
- **WHEN** the caller sends both name sets with valid values
- **THEN** the server uses pageSize and pageToken and ignores limit and cursor

#### Scenario: Invalid ignored value ignored
- **WHEN** the caller sends valid pageSize and pageToken with an invalid limit or cursor
- **THEN** the server answers the page for the valid winning values without a 400

### Requirement: Paging defaults and maximum

When a caller omits the page size on an in-scope list route, the server SHALL use the current default page size of the route.

#### Scenario: Missing page size
- **WHEN** the caller sends no pageSize and no limit
- **THEN** the server uses the current default page size of the route

#### Scenario: Over-maximum refused
- **WHEN** the caller sends a page size above the current maximum of the route
- **THEN** the server answers 400 with the route error code

### Requirement: Deprecated paging response

When a renamed list route has another page, the server SHALL return nextPageToken alongside nextCursor with the same value.

#### Scenario: Both tokens returned
- **WHEN** a renamed list route has another page
- **THEN** the server returns nextPageToken and nextCursor with the same value

### Requirement: Store-side provider paging

When a caller lists registry providers, the server SHALL read at most limit plus one provider address rows from the store.

#### Scenario: Second page
- **WHEN** the caller lists providers with a page token
- **THEN** the server returns the next addresses and a next page token

#### Scenario: Removed address between pages
- **WHEN** the token address was removed before the next page read
- **THEN** the server returns the addresses after the token address in sort order

#### Scenario: Exact last page
- **WHEN** the remaining rows equal the requested page size
- **THEN** the server returns no next page token

#### Scenario: Same order on each dialect
- **WHEN** the caller pages the provider list
- **THEN** the server returns the same address order on each store dialect

### Requirement: Forged provider token

When a caller sends a forged provider page token, the server SHALL refuse the call with code registry.invalid.

#### Scenario: Forged token refused
- **WHEN** the caller sends a page token that is not a valid provider address of the namespace
- **THEN** the server answers 400 with code registry.invalid without a store address read

### Requirement: Provider token codec

When the server makes a registry provider page token, the server SHALL encode it with internal/pagetoken.

#### Scenario: Shared codec
- **WHEN** the caller pages the provider list
- **THEN** the server encodes and strictly decodes the token with internal/pagetoken within its size limit

### Requirement: Provider namespace check

When the namespace name of a Providers call is invalid, the server SHALL refuse the call before the store call.

#### Scenario: Invalid namespace refused
- **WHEN** the caller passes an invalid namespace name to Providers
- **THEN** the server answers 400 without calling the store

#### Scenario: Unknown namespace
- **WHEN** the caller lists providers of a valid but unknown namespace
- **THEN** the server answers the not-found error of the route

#### Scenario: Denied caller refused
- **WHEN** the caller lacks the read grant on the namespace
- **THEN** the server answers 403 without reading provider addresses

### Requirement: Paging query parameters declared

Where a tool-neutral list route pages, the server SHALL declare pageSize and pageToken in the API description.

#### Scenario: Parameters listed
- **WHEN** the caller reads the API description of a renamed list route
- **THEN** openapi.json lists pageSize and pageToken on the route with limit and cursor marked deprecated
