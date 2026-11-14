## ADDED Requirements

### Requirement: shared-hostile-export

When a dev-data hostile-body group starts, the test setup SHALL build one export and one staged area and vary only the approvers row in each case copy.

#### Scenario: each-varied-row-reaches-strip-and-swap
- **WHEN** the group reuses one built export and varies only the approvers row per staged copy
- **THEN** each hostile case still fails closed and the strip and the swap read the varied approvers row

### Requirement: template-copy-isolation

When a store test needs a fresh database, the test setup SHALL copy one migrated template database and remove the copy after the test.

#### Scenario: template-copy-keeps-no-shared-mutation
- **WHEN** cases run in parallel and one case fails halfway
- **THEN** no mutation in one copy shows in the template, in the shared export, or in another copy

#### Scenario: template-failure-fails-package-setup
- **WHEN** template migration or copy fails
- **THEN** package setup fails without running store cases

#### Scenario: wall-time-drops-with-same-assertions
- **WHEN** template copies replace per-case migrations
- **THEN** the sqlite suite wall time drops and all assertions stay unchanged
