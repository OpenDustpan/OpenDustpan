# terragrunt-local-run-all Specification

## Purpose
This capability covers a developer who runs Terragrunt run-all on a local machine against the server. Each Terragrunt unit uses its own workspace with its own lock, state versions and history. The e2e tests run the real Terragrunt binary.

## Requirements

### Requirement: per-unit workspaces

When a developer runs terragrunt run-all against the server, the server SHALL serve each Terragrunt unit from its own workspace with its own lock, state versions and history.

#### Scenario: three units apply
- **WHEN** a developer runs terragrunt run-all apply over 3 pre-created unit workspaces
- **THEN** each of the 3 workspaces holds 1 new state version and 1 history entry, and no state version holds resources from another unit

### Requirement: real Terragrunt in e2e

The e2e tests SHALL run the real terragrunt run-all through the pinned Terragrunt binary of the e2e fetcher.

#### Scenario: e2e uses the pinned binary
- **WHEN** an e2e test runs a local run-all
- **THEN** it installs the binary with fetcher.Install(ctx, e2e.Terragrunt(), version), sets the OpenTofu binary of the fetcher as TG_TF_PATH and calls no tofu directly in place of Terragrunt

### Requirement: locked workspace refuses second writer

When two applies target one locked workspace at the same time, the server SHALL refuse the second writer with the workspace.locked error.

#### Scenario: second writer gets lock error
- **WHEN** two applies target one locked workspace at the same time
- **THEN** the server returns the workspace.locked error for the second writer and the first apply keeps its lock

### Requirement: unknown workspace fails

When a unit names a workspace that does not exist, the server SHALL return the workspace.not-found error.

#### Scenario: wrong workspace name fails
- **WHEN** a unit names a workspace that does not exist
- **THEN** the server returns the workspace.not-found error with no new state version

### Requirement: shared name serializes

When two unit directories name the same workspace, the server SHALL keep both writes in that one workspace chain under that one workspace lock.

#### Scenario: two units share one name
- **WHEN** two unit directories name the same workspace at parallelism 3 with no lock timeout
- **THEN** both writes land in the one workspace chain and the second writer gets the workspace.locked error

### Requirement: unit without grant refused

When the token of a run holds no grant on one unit workspace, the server SHALL refuse that unit while it serves the granted units.

#### Scenario: one unit refused
- **WHEN** the token of a run holds no grant on one of the three unit workspaces
- **THEN** the server refuses the ungranted unit and records one new state version in each of the other two workspaces
