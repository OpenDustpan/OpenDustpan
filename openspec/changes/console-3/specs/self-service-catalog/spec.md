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

#### Scenario: Paged catalog
- **WHEN** the developer moves to the next page of a large catalog
- **THEN** the catalog shows the next page with stable order kept

### Requirement: Show auth refusals

When a catalog or create request answers 401 or 403, the console SHALL show a sign-in or denied message.

#### Scenario: Expired session
- **WHEN** the catalog or create request answers 401
- **THEN** the console shows a sign-in message with a link to sign in

#### Scenario: Missing grant
- **WHEN** the catalog or create request answers 403
- **THEN** the console shows a denied message that names the missing read or create grant

### Requirement: Show input schema

When the developer opens the create form of one blueprint version, the console SHALL show each input of that version in server order.

#### Scenario: Versioned inputs in order
- **WHEN** the developer opens the create form of one blueprint version
- **THEN** the create form shows each input of that version in server order and hides each inapplicable input

### Requirement: Show raw JSON mode

When the developer opens raw JSON mode, the console SHALL show the raw request JSON and the raw server answer.

#### Scenario: Raw mode shown
- **WHEN** the developer opens raw JSON mode
- **THEN** the form shows the raw request JSON and the raw server answer

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

#### Scenario: Missing blueprint
- **WHEN** the blueprint no longer exists at submit
- **THEN** the console shows a not-found message with a link back to the catalog

#### Scenario: Outage retry
- **WHEN** the list or create request fails with a server outage
- **THEN** the console shows a retry message with the request ID

### Requirement: Keep Idempotency-Key lifecycle

When the developer retries an unchanged create request, the console SHALL resend the same Idempotency-Key.

#### Scenario: Retry reuses key
- **WHEN** the first send times out before an answer
- **THEN** the console resends the same Idempotency-Key with no second unit request

#### Scenario: Changed body conflicts
- **WHEN** the same key is resent with a changed body
- **THEN** the console shows the 409 conflict text unchanged and keeps the user values

### Requirement: Show stale version

When the blueprint version changes between form open and submit, the console SHALL show a stale-schema message.

#### Scenario: Stale schema
- **WHEN** the blueprint version changes between form open and submit
- **THEN** the console shows a stale-schema message with a link to reload the new version
