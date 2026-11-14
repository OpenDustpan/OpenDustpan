## ADDED Requirements

### Requirement: Refuse accessors before Start

When Server.Start has not succeeded, the server SHALL refuse each call of Server.Workspaces, Server.Backups and Server.Restores with the NEW error ErrNotStarted.

#### Scenario: Workspaces before Start
- **WHEN** An embedder calls Server.Workspaces before Server.Start has succeeded.
- **THEN** The accessor refuses with the NEW error ErrNotStarted.

#### Scenario: Backups before Start
- **WHEN** An embedder calls Server.Backups before Server.Start has succeeded.
- **THEN** The accessor refuses with the NEW error ErrNotStarted.

#### Scenario: Restores before Start
- **WHEN** An embedder calls Server.Restores before Server.Start has succeeded.
- **THEN** The accessor refuses with the NEW error ErrNotStarted.

#### Scenario: Call after Start
- **WHEN** Server.Start has succeeded.
- **THEN** The accessor returns the service for use.

#### Scenario: Call after failed Start
- **WHEN** Server.Start has failed.
- **THEN** The accessor refuses with the NEW error ErrNotStarted.
