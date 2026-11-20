## ADDED Requirements

### Requirement: Approval routes at collection and unit scope

The server SHALL serve list, get, set and delete of approval rules, and get, set and reset of the approval default, at the collection scope and at the by-name unit scope. The read routes SHALL declare `namespace:read-gates`. The write routes SHALL declare `namespace:manage-gates` and SHALL need a fresh authentication.

#### Scenario: List at a collection
- **WHEN** a holder of the read action lists the approval rules of a collection
- **THEN** the server answers the rules stored at that collection only, and no rule of the namespace

#### Scenario: Unit path
- **WHEN** a holder of the read action reads an approval rule of the stack web/prod through the by-name path with the name encoded
- **THEN** the server answers the rule of that unit

#### Scenario: Rule of another scope
- **WHEN** a caller reads the ID of a namespace rule through a collection path
- **THEN** the server answers 404 with code gate.not-found

#### Scenario: No action
- **WHEN** a caller without the manage action saves an approval rule at a collection
- **THEN** the server answers 403 with code access.denied and stores nothing

#### Scenario: Access token write
- **WHEN** a caller with an access token saves an approval default at a unit
- **THEN** the server answers 403 with code mfa.fresh-step-required and the stored default does not change

#### Scenario: Reset without a default
- **WHEN** a manager resets the approval default of a unit that has none
- **THEN** the server answers 204

### Requirement: Versions and limits at the new scopes

A write at the collection or the unit scope SHALL follow the rules of the namespace scope: a missing If-Match uses the stored version, a stale version gives 409 with code gate.version-conflict, a body with an unknown field gives 400, and a scope holds at most 64 approval rules.

#### Scenario: Stale version
- **WHEN** a manager saves an approval rule at a collection with an If-Match that is not the stored version
- **THEN** the server answers 409 with code gate.version-conflict and the rule does not change

#### Scenario: Missing If-Match
- **WHEN** a manager saves an approval rule at a unit with no If-Match header
- **THEN** the server replaces the stored rule, or creates it

#### Scenario: Rule limit
- **WHEN** a manager saves the 65th approval rule of a collection
- **THEN** the server answers 422 with code gate.limit and stores nothing

#### Scenario: Final rule
- **WHEN** a manager saves a rule at a unit with the ID of a final rule of the namespace
- **THEN** the server answers 409 with code gate.final and stores nothing

#### Scenario: Unknown field
- **WHEN** a manager sends a body with the field `scope`
- **THEN** the server answers 400 and calls no store method

### Requirement: Stable unit key for approval rules

The approval rules and the approval default of a unit SHALL stay with the stable unit ID. The by-name path SHALL resolve to the ID inside the request.

#### Scenario: Rename
- **WHEN** a unit with an approval rule is renamed
- **THEN** a read through the new name answers the rule

#### Scenario: Reused name
- **WHEN** a unit is deleted and a new unit gets its name
- **THEN** the new unit has no approval rule

#### Scenario: Unknown unit
- **WHEN** a manager saves a rule for a unit that does not exist
- **THEN** the server answers 404 with code unit.not-found

### Requirement: Audit of a scope write

A write of an approval rule or an approval default at a collection or a unit SHALL write an audit entry in the same transaction. The entry SHALL name the caller, the action and the scope key.

#### Scenario: Audit entry
- **WHEN** a manager saves an approval rule at a collection
- **THEN** the audit log holds an entry with the action `namespace:set-approval-rule` and the scope key of the collection row

#### Scenario: Audit failure
- **WHEN** the audit write fails
- **THEN** the rule does not change

### Requirement: Installation read routes

The server SHALL serve list and get of gates, access limits and approval rules at the installation scope under `/api/dustpan/v1`. The routes SHALL declare `installation:read-gates` on the object `@server`. The service SHALL accept the installation scope for these reads and SHALL read no namespace.

#### Scenario: Installation read
- **WHEN** a holder of the installation read action lists the gates of the installation
- **THEN** the server answers the gates stored at the installation only

#### Scenario: Namespace holder
- **WHEN** a holder of only `namespace:read-gates` lists the gates of the installation
- **THEN** the server answers 403 with code access.denied

#### Scenario: Unknown ID
- **WHEN** a holder of the installation read action reads an ID that is not stored at the installation
- **THEN** the server answers 404 with code gate.not-found

### Requirement: Installation writes stay refused

Until a decision names the holders of an installation manage action, the service SHALL refuse a write of any gate configuration kind at the installation scope, and an approval default at the installation scope, with `gate.ErrInvalid`. The service SHALL refuse a blueprint scope, a profile scope and the empty scope for every use.

#### Scenario: Installation write
- **WHEN** a caller of the service saves an approval rule at the installation scope
- **THEN** the service returns `gate.ErrInvalid` and calls no store method

#### Scenario: Installation approval default
- **WHEN** a caller of the service sets an approval default at the installation scope
- **THEN** the service returns `gate.ErrInvalid`

#### Scenario: Empty scope
- **WHEN** a caller of the service reads with the empty scope
- **THEN** the service returns `gate.ErrInvalid`
