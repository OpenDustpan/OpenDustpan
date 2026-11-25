# database-outage-probe Specification

## Purpose
The outage probe shows that each server instance answers 503 with Retry-After while the database is down, that Pulumi and OpenTofu commands end clean after the database returns, and how long each CLI version retries a write.

## Requirements

### Requirement: long-outage-probe

When the database is stopped for 60 s during tofu apply and pulumi up, the server SHALL answer 503 on readiness and 503 with Retry-After on writes on each instance and accept the same work again after the database returns.

#### Scenario: Long outage readiness and writes
- **WHEN** the suite stops PostgreSQL for 60 s during tofu apply and pulumi up
- **THEN** the probe records that each instance answered 503 on readiness and 503 with Retry-After on writes during the outage and turned ready with no restart after the database returned

#### Scenario: Pulumi cancel during outage
- **WHEN** the operator cancels the stopped Pulumi update while the database is still down
- **THEN** the stopped Pulumi update stays running until cancel ends it, and a cancel that fails keeps the update running with a 503 message

#### Scenario: No half-written state version
- **WHEN** a tofu apply dies mid-state-write during the outage
- **THEN** the version list before and after shows no new committed version from the failed command

#### Scenario: No leftover workspace lock
- **WHEN** a tofu apply fails during the outage
- **THEN** the lock read before and after shows no lock on the workspace after the failed apply

#### Scenario: Recovery shows no changes
- **WHEN** the database returns after the long outage
- **THEN** rerun tofu apply and pulumi up complete and the next preview and plan show no changes with the secret intact

### Requirement: retry-report

When the long outage measures one write retry per CLI version, the server SHALL let the probe record the retry seconds per CLI version and per write kind for the short window.

#### Scenario: Per-version retry report feeds short window
- **WHEN** the suite measures how long one write retries before the command fails in the long outage
- **THEN** the probe writes seconds per CLI version and per write kind (checkpoint, events, complete and the OpenTofu state version), writes 0 s for a kind that the CLI does not retry, and sets the short stop to 50 percent of the smallest value above 0 s in the same run

### Requirement: short-outage-probe

When the database is stopped for 50 percent of the smallest measured retry window above 0 s, a command whose write kind has a window above 0 s SHALL end with exit 0 and no leftover lock, and a command whose write kind has a window of 0 s SHALL fail clean and succeed on the next run.

#### Scenario: Short outage completes for a retried write kind
- **WHEN** the suite stops PostgreSQL for 50 percent of the smallest measured retry window above 0 s during a command whose write kind has a window above 0 s
- **THEN** the command ends with exit 0 with no manual retry, no half-written state version, and no leftover lock

#### Scenario: Short outage fails clean for a write kind that is not retried
- **WHEN** the suite stops PostgreSQL for the same short window during a command whose write kind has a window of 0 s, such as the Pulumi checkpoint save
- **THEN** the command exits non-zero, the state is not corrupt, the lock is released, and the next run of the same command ends with exit 0 and exactly one new state version
