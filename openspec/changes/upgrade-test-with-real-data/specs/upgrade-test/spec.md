## ADDED Requirements

### Requirement: real-data-upgrade

When SeedOldRelease seeds a supported old release and VerifyAfterUpgrade checks the new binary, the server SHALL keep each stack, state version, secret, grant, audit entry and held lock readable and writable.

#### Scenario: Seed then verify
- **WHEN** SeedOldRelease seeds stacks, workspaces, secrets, grants, audit entries and one held lock with both CLIs, stops the old binary, and the new binary starts on the same database and key file
- **THEN** VerifyAfterUpgrade reads each object back equal, decrypts each secret to plain text, checks principals of each kind, agent-block audit entries and one finished key rotation, and each CLI completes one more write

#### Scenario: Empty database
- **WHEN** no tag exists and the database is empty
- **THEN** the new binary starts with no refusal and writes the first stack and workspace

### Requirement: skip-release-upgrade

When the database holds a supported old schema, the server SHALL start directly with no release in between.

#### Scenario: Direct start
- **WHEN** the database holds the pinned commit schema
- **THEN** the migration runner applies each missing migration in order

### Requirement: old-binary-refusal

When an old binary starts on a database schema newer than it knows, the server SHALL refuse to start.

#### Scenario: Old binary after upgrade
- **WHEN** the old binary starts on the upgraded database
- **THEN** the process exits non-zero and its log reports a newer schema

### Requirement: held-lock-survives

While a workspace lock is held across the upgrade, the server SHALL keep the lock held by its holder.

#### Scenario: Held lock blocks
- **WHEN** a workspace lock is held across the upgrade
- **THEN** a second writer gets `workspace.locked` and the holder can unlock

### Requirement: upgrade-test-separate

Where the upgrade suite runs, the server SHALL keep `make upgrade-test` outside `make check`.

#### Scenario: Missing dependency
- **WHEN** PostgreSQL is not reachable or a CLI binary is missing
- **THEN** the fixture stops and names the missing dependency

### Requirement: operator-upgrade-docs

When an operator follows the upgrade docs page, the server SHALL upgrade with backup, stop, start and check steps that the fixture runs as written.

#### Scenario: Docs steps run in test
- **WHEN** an operator follows the upgrade docs page backup, stop, start new binary and check steps through the docs-commands corpus
- **THEN** the fixture passes with no extra step
