## ADDED Requirements

### Requirement: Restore refuses over-cap owners

When a staged backup holds more than MaxPerOwner views for one owner in one namespace, the server SHALL fail the restore with backup.ErrCorrupt by checkStagedSavedViews.

#### Scenario: Owner over cap refused
- **WHEN** a staged backup holds MaxPerOwner + 1 views for one owner in one namespace
- **THEN** the restore fails with backup.ErrCorrupt before any staged saved-view row is applied

#### Scenario: Owner over cap in second namespace refused
- **WHEN** a staged backup holds MaxPerOwner views in one namespace and MaxPerOwner + 1 views for the same owner in a second namespace
- **THEN** the restore fails with backup.ErrCorrupt before any staged saved-view row is applied

#### Scenario: Owner at cap passes
- **WHEN** a staged backup holds exactly MaxPerOwner views for one owner in one namespace
- **THEN** checkStagedSavedViews accepts the staged rows

### Requirement: Restore refuses over-cap namespace

When a staged backup holds more than MaxPerNamespace views in one namespace, the server SHALL fail the restore with backup.ErrCorrupt by checkStagedSavedViews.

#### Scenario: Namespace over cap refused
- **WHEN** a staged backup holds MaxPerNamespace + 1 views in one namespace
- **THEN** the restore fails with backup.ErrCorrupt before any staged saved-view row is applied

#### Scenario: Namespace at cap passes
- **WHEN** a staged backup holds exactly MaxPerNamespace views in one namespace
- **THEN** checkStagedSavedViews accepts the staged rows

#### Scenario: Both caps breached refused
- **WHEN** a staged backup breaches both the owner cap and the namespace cap
- **THEN** the restore fails with backup.ErrCorrupt before any staged saved-view row is applied
