## ADDED Requirements

### Requirement: Show catalog

When the developer opens the catalog of a namespace, the console SHALL show each published blueprint name and description of that namespace.

#### Scenario: Open catalog
- **WHEN** the developer opens the catalog of a namespace
- **THEN** the catalog shows each published blueprint name and description of the namespace

#### Scenario: Empty catalog
- **WHEN** the namespace holds no published blueprint
- **THEN** the catalog shows an empty state with a link to the namespace home

#### Scenario: Unpublished hidden
- **WHEN** the developer opens the catalog
- **THEN** the catalog hides each unpublished and draft blueprint

### Requirement: Show input schema

When the developer opens the create form of one blueprint version, the console SHALL show each input of that version in server order.

#### Scenario: Versioned inputs in order
- **WHEN** the developer opens the create form of one blueprint version
- **THEN** the create form shows each input of that version in server order and hides each inapplicable input

### Requirement: Show input errors unchanged

When a create request fails with a per-input error that names the input, the console SHALL show each server message unchanged as escaped plain text next to that input.

#### Scenario: Bad input shown
- **WHEN** the create request fails with a per-input error that names the input
- **THEN** the form shows each server message unchanged as escaped plain text next to that input

#### Scenario: Hostile text escaped
- **WHEN** an input value or error holds script text or an oversized value
- **THEN** the form runs no script and shows the text as plain text

### Requirement: Send stable ID request

When the developer sends a create request, the console SHALL send the blueprint stable ID with one Idempotency-Key.

#### Scenario: Valid request sent
- **WHEN** the developer sends a valid create request
- **THEN** the console sends one request with the blueprint stable ID and one Idempotency-Key and shows the new unit reference

#### Scenario: Retry reuses key
- **WHEN** the first send times out before an answer
- **THEN** the console resends the same Idempotency-Key with no second unit request
