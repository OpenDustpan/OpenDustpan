## ADDED Requirements

### Requirement: Show limits as effective values

Where a namespace owns releases, the server SHALL show the request and unpack limits as effective values in the API, read from the same value that the checks use.

#### Scenario: Limits visible
- **WHEN** a caller with the read grant reads the registry limits of a namespace.
- **THEN** the API reports the request limit, the unpacked size limit, the file count limit, the size limit of one file and the expansion limit, each as a number with its unit.

#### Scenario: Shown value equals the check
- **WHEN** a publish exceeds a limit that the route shows.
- **THEN** the refusal names the same value that the route shows.

#### Scenario: Limits refused without the grant
- **WHEN** a caller without the read grant reads the registry limits.
- **THEN** the server refuses with 403 and shows no value.
