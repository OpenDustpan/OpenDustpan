## ADDED Requirements

### Requirement: timeZone embedded check

When a person saves a timeZone preference, the server SHALL accept the value only when that value names a zone in the embedded time zone database.

#### Scenario: Host-only zone refused
- **WHEN** A person patches timeZone to a name that exists only on the host and not in the embedded database.
- **THEN** The server answers 422 with preferences.field-invalid and stores nothing.

#### Scenario: Embedded zone accepted
- **WHEN** A person patches timeZone to Europe/Berlin in the embedded database.
- **THEN** The server stores the value and later reads return it.

#### Scenario: Same answer under ZONEINFO
- **WHEN** A person patches timeZone to the same name under each host setup.
- **THEN** The server gives the same accept or refuse answer with ZONEINFO set, unset, and with a host zone directory present or absent.

### Requirement: timeZone refuses local and shaped names

When a person saves a timeZone preference, the server SHALL refuse a host-local name, a wrong-case name, an empty value, and a value that fails zoneShape.

#### Scenario: Local names refused
- **WHEN** A person patches timeZone to Local, localtime, posixrules, posix/Europe, or right/UTC.
- **THEN** The server answers 422 with preferences.field-invalid.

#### Scenario: Bad shape and case refused
- **WHEN** A person patches timeZone to europe/paris, an empty string, or a name with empty segments or other bytes.
- **THEN** The server answers 422 with preferences.field-invalid.

#### Scenario: UTC accepted
- **WHEN** A person patches timeZone to UTC.
- **THEN** The server stores UTC.

### Requirement: timeZone stored values survive

While a stored timeZone value predates the fix, the server SHALL keep returning it and keep it through restore and import without rechecking it.

#### Scenario: Old value stays readable
- **WHEN** A stored timeZone predates the fix and is absent from the new allowlist.
- **THEN** Reads return the stored value unchanged.

#### Scenario: Other fields patch freely
- **WHEN** A person patches another preferences field while an old timeZone value is stored.
- **THEN** The server applies the other fields and keeps the stored timeZone unchanged.

#### Scenario: Repeat stored value accepted
- **WHEN** A person patches timeZone to the same value that is already stored.
- **THEN** The server keeps the stored timeZone unchanged without rechecking it.

#### Scenario: Restore and import keep value
- **WHEN** A backup restore or an import carries a stored timeZone that predates the fix.
- **THEN** Reads return the stored value unchanged.
