## ADDED Requirements

### Requirement: Patch refuses locked switch

When the static layer locks a declared module switch off, the server SHALL refuse a PATCH that touches the switch.

#### Scenario: Locked PATCH refused
- **WHEN** a caller with an access token sends PATCH /api/dustpan/v1/settings that sets a locked switch on
- **THEN** the server answers 409 with code settings.locked, names the key and the static layer, and changes no value

#### Scenario: Mixed PATCH all or none
- **WHEN** a PATCH holds one locked key and one unlocked key
- **THEN** the server answers 409 with code settings.locked and changes no key

#### Scenario: Same off value still refused
- **WHEN** a PATCH sets a locked-off switch to off again
- **THEN** the server answers 409 with code settings.locked

#### Scenario: Lock removed
- **WHEN** the operator removes the lock from the file and reloads
- **THEN** the server accepts the PATCH after the applied reload bumps the revision

### Requirement: Get shows lock

When the static layer locks a module switch off, the server SHALL answer the switch with locked true and source static.

#### Scenario: Locked flag shown
- **WHEN** a caller reads GET /api/dustpan/v1/settings while the lock holds
- **THEN** the switch shows locked true with source static and the console shows no control

#### Scenario: Namespace read under install lock
- **WHEN** a caller reads GET /api/dustpan/v1/namespaces/{namespace}/settings while the installation locks it
- **THEN** the namespace settings read shows the switch off with locked true and source static

#### Scenario: Namespace PATCH under install lock
- **WHEN** a caller sends PATCH to the namespace settings that turns the locked switch on
- **THEN** the server answers 409 with code settings.locked and changes nothing

#### Scenario: Namespace off while install on
- **WHEN** the installation leaves the switch on and the namespace turns it off
- **THEN** the server accepts the namespace PATCH and the namespace reads off

### Requirement: Capabilities and module routes off

When the static layer locks a module switch off, the server SHALL list the module as off in the capabilities document.

#### Scenario: Module listed off
- **WHEN** a signed-in caller with caller read reads the capabilities document while the lock holds
- **THEN** the module shows enabled false

#### Scenario: Anonymous version only
- **WHEN** an anonymous caller reads the capabilities document
- **THEN** the answer holds apiVersion with no modules field

#### Scenario: No caller read
- **WHEN** a signed-in caller without the caller read grant reads the capabilities document
- **THEN** the answer holds apiVersion with no modules field

#### Scenario: Module route refused
- **WHEN** a signed-in caller calls a route of the locked-off module
- **THEN** the route answers 404 with the NEW error code `module.off` after the sign-in check

### Requirement: Stored snapshot and divergent instance

While two instances share one database, the server SHALL answer and lock from the stored static snapshot.

#### Scenario: Divergent file fails closed
- **WHEN** a second instance calls Sync with a distinct static digest
- **THEN** the divergent instance fails readiness and refuses writes with 503 settings.unavailable plus Retry-After 30

#### Scenario: Reload bumps revision
- **WHEN** the operator changes the file and the reload applies
- **THEN** the revision bumps and an audit entry records the changed keys

### Requirement: Security switch reports

When a lock turns a security switch off, the server SHALL write the start reports.

#### Scenario: Start reports
- **WHEN** a lock turns a security switch off
- **THEN** the start writes one WARN line, one @server audit entry, one per-namespace entry on first apply, and one admin report row
