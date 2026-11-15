## ADDED Requirements

### Requirement: No baseline hash file before the first tag

Until the first tag, the tree SHALL hold no `.sql.sha256` file next to a baseline of a store dialect, and no script SHALL write one.

#### Scenario: A pull request edits a baseline
- **WHEN** two pull requests each edit the baseline of the same dialect
- **THEN** neither changes a hash file, and the runner still records the SHA-256 of the baseline in `schema_migrations`

#### Scenario: The store packages build
- **WHEN** the store tests of each dialect build with no hash file in `migrations/`
- **THEN** the build passes, and `TestFrozenMigrations` stays off until the release record lists a shipped migration
