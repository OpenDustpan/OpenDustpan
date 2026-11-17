## ADDED Requirements

### Requirement: serve releases under slug

Where a namespace owns releases, the server SHALL serve its releases under the slug formed as the lowercase stable ID with unsafe characters removed.

#### Scenario: Slug install
- **WHEN** a namespace holds a name outside the registry-safe subset of lowercase letters, digits, and hyphen.
- **THEN** clients fetch the release under the slug path with 200.

#### Scenario: Slug stable after rename
- **WHEN** a namespace renames its display name.
- **THEN** the registry slug path stays identical after the rename and the prior name path still serves existing releases.

#### Scenario: Unknown slug refused
- **WHEN** a client requests a slug that maps to no namespace.
- **THEN** the server refuses with 404 registry.not-found.

#### Scenario: Slug collision resolved
- **WHEN** two namespace IDs strip to the same slug or to an empty string.
- **THEN** the second namespace receives the slug with a short suffix and each slug maps to one namespace.

#### Scenario: Slug wins on conflict
- **WHEN** a slug equals the prior name path of another namespace.
- **THEN** the slug path serves the slug owner and the prior name path serves existing releases of the other namespace.
