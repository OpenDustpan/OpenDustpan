## ADDED Requirements

### Requirement: failed-export-audit

If a collection access review request fails after the authorization check, then the server SHALL write one audit entry of kind `access`.

#### Scenario: missing collection
- **WHEN** a caller with the read right reviews a missing collection through collectionReviewScope
- **THEN** the server answers `collection.not-found` and writes one audit entry

#### Scenario: bad page token
- **WHEN** a caller sends a page token that fails its check
- **THEN** the server refuses the request and writes one audit entry

#### Scenario: denied caller
- **WHEN** a caller without the read right reviews a collection
- **THEN** the server denies the request and writes no audit entry of this kind

#### Scenario: audit write fails
- **WHEN** the audit write fails on a failed request
- **THEN** the server answers 500

### Requirement: paged-export-snapshot

When a request carries a page token, the server SHALL read the units of the collection from the token and not from the store.

#### Scenario: membership change
- **WHEN** a unit moves out of the collection between two pages read through collectionReviewScope
- **THEN** the second page still uses the unit list of the first page

#### Scenario: growth past the cap
- **WHEN** a collection grows past 2000 units between two pages
- **THEN** the second page answers from the token with no error

#### Scenario: empty collection
- **WHEN** a collection holds no unit
- **THEN** the server answers an empty first page with no error

### Requirement: collection-unit-cap

When a collection holds more than 2000 units, the server SHALL refuse its access review with the error code `access.collection-unit-cap`.

#### Scenario: at the cap
- **WHEN** a collection holds exactly 2000 units
- **THEN** the server answers the first page

#### Scenario: over the cap
- **WHEN** a collection holds 2001 units
- **THEN** the server answers `access.collection-unit-cap` with status 422 and writes one audit entry

#### Scenario: default collection over the cap
- **WHEN** the default collection of a namespace holds more than 2000 units
- **THEN** the server answers `access.collection-unit-cap` and the text names the namespace access review route
