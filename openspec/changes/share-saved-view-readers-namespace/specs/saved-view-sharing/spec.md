## ADDED Requirements

### Requirement: Share with namespace readers

When a saved view has visibility namespace, the server SHALL answer the view to each reader of the namespace.

#### Scenario: Reader reads a shared view
- **WHEN** a reader of the namespace reads a view with visibility namespace
- **THEN** the server answers the view

#### Scenario: Non-member refused
- **WHEN** a principal with no read of the namespace reads the view
- **THEN** the server answers 404 with view.not-found

### Requirement: Share with a collection

When a saved view has visibility collection, the server SHALL answer the view only to readers of the collection that the view names by its ID.

#### Scenario: Outsider cannot read
- **WHEN** a principal outside the collection reads the view or lists the views
- **THEN** the server answers 404 on the view and its units route, and the list omits it

#### Scenario: Collection of another namespace
- **WHEN** a share names a collection of another namespace
- **THEN** the server answers 404 with view.collection-not-found and changes nothing

#### Scenario: Collection deleted
- **WHEN** the collection of a shared view is deleted
- **THEN** the view becomes private and the server writes an audit entry

#### Scenario: Unreadable collection
- **WHEN** a sharer names a collection that the sharer cannot read
- **THEN** the server answers 404 with view.not-found

### Requirement: No access from a view

While a reader runs the units of a shared view, the server SHALL check unit:read for each unit with the access of the reader.

#### Scenario: Hidden unit stays hidden
- **WHEN** a reader runs the units route of a shared view
- **THEN** the route omits each unit that the reader cannot read

### Requirement: Share action and audit

When a principal changes the visibility of a saved view, the server SHALL require the action view:share and write an audit entry.

#### Scenario: Share writes audit
- **WHEN** a holder of view:share shares a view
- **THEN** the server writes an audit entry with the view ID and the new visibility

#### Scenario: Reader without the action
- **WHEN** a caller who can read the view but lacks view:share shares it
- **THEN** the server answers 403 with view.share-denied and writes an audit entry

#### Scenario: Unshare
- **WHEN** the maker sets the visibility to private
- **THEN** the server hides the view from other readers and writes an audit entry

#### Scenario: Replay
- **WHEN** a share repeats the same Idempotency-Key
- **THEN** the server answers the first answer and writes no second entry

#### Scenario: Concurrent writers
- **WHEN** a share carries a stale If-Match after a replace
- **THEN** the server answers 412 with view.version-mismatch and changes nothing

### Requirement: Replace with a match

When the maker renames or replaces a saved view, the server SHALL require If-Match, the fence of the namespace and an audit entry.

#### Scenario: Stale version
- **WHEN** a rename carries a stale If-Match version
- **THEN** the server answers 412 with view.version-mismatch

#### Scenario: Missing header
- **WHEN** a replace has no If-Match header
- **THEN** the server answers 428 with view.if-match-required

#### Scenario: Missing view
- **WHEN** a replace names a view that does not exist
- **THEN** the server answers 404 with view.not-found

#### Scenario: Invalid query
- **WHEN** a replace holds a query that fails Validate
- **THEN** the server answers 400 with view.query-invalid and keeps the stored view

#### Scenario: Not the maker
- **WHEN** a namespace administrator replaces the view of another person
- **THEN** the server answers 403 and keeps the stored view

### Requirement: Delete by an administrator

When a namespace administrator deletes a view, the server SHALL delete the view and write an audit entry.

#### Scenario: Administrator deletes a shared view
- **WHEN** a namespace administrator deletes a shared view of another person
- **THEN** the server deletes the view and writes an audit entry

### Requirement: Old rows

If a stored view fails validation on read, then the server SHALL refuse to serve the view and write an audit entry.

#### Scenario: Old row has no visibility
- **WHEN** a stored view has no visibility
- **THEN** the server reads it as private

#### Scenario: Bad row
- **WHEN** a stored row fails Validate
- **THEN** the server answers 404 and writes an audit entry
