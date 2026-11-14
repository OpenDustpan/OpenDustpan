## ADDED Requirements

### Requirement: Refuse userinfo and blocked scheme

When a webhook destination holds userinfo or a scheme outside http and https, the server SHALL refuse the destination.

#### Scenario: Write with userinfo
- **WHEN** a sink write carries a webhook URL with userinfo
- **THEN** the route answers sink.invalid and stores nothing

#### Scenario: Open with valid URL
- **WHEN** the destination uses http or https with no userinfo
- **THEN** OpenDestination returns the URL

### Requirement: Omit corrupt legacy row

When a legacy sink row holds userinfo and migrates to an empty target, the server SHALL omit the sink from the list.

#### Scenario: List after migration
- **WHEN** a legacy userinfo row migrates
- **THEN** the list omits the sink and open fails with ErrNoURL

### Requirement: Restore corrupt row

When staged restore data holds a sink row with an empty target, the server SHALL fail the restore with ErrCorrupt.

#### Scenario: Apply on empty target
- **WHEN** staged data holds a sink row with an empty target
- **THEN** the restore fails with ErrCorrupt, records the NEW error code `backup.corrupt`, and names the sink
