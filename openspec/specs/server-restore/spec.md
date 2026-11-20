# server-restore Specification

## Purpose
A restore replaces the access tables only when the caller holds the action server:restore-access. This capability covers the installation setting restore.replace-access-tables, which lets each holder of server:restore replace them. It covers what a restore does to the access tables and to the setting row, and the audit entries of the restore. It does not cover how a person signs in or how the server stores a setting.

## Requirements

### Requirement: Restore access setting default

While the installation setting is off, the server SHALL refuse to replace the access tables on a restore by a caller without server:restore-access.

#### Scenario: Default off refusal
- **WHEN** the setting is off and a caller with only server:restore restores a backup with access tables
- **THEN** the server keeps the access tables unchanged

### Requirement: Setting change writer rule

When a caller with server:manage-settings plus server:restore-access changes the setting with a fresh session step, the server SHALL save the new value.

#### Scenario: Allowed change saves
- **WHEN** an allowed caller changes the setting with a fresh session step
- **THEN** the server saves the new value

#### Scenario: Refused change
- **WHEN** a caller changes the setting without a fresh step or without both actions
- **THEN** the server refuses, keeps the value and writes no setting-change entry

### Requirement: Setting change audit

When a saved change of the setting commits, the server SHALL write an audit entry with the old and the new value.

#### Scenario: Change recorded
- **WHEN** a saved change commits
- **THEN** the audit entry holds the old and the new value

### Requirement: Restore with setting on

While the setting is on at apply start, the server SHALL let a caller with server:restore replace the access tables on restore.

#### Scenario: Widened restore
- **WHEN** the setting is on and a caller with server:restore restores
- **THEN** the server replaces the access tables

### Requirement: Widened restore keeps two paths

When a restore replaces the access tables, the server SHALL refuse a result with fewer than two working break-glass paths.

#### Scenario: Last path refused
- **WHEN** a widened restore leaves fewer than two paths
- **THEN** the restore fails with the code principal.no-working-break-glass and changes nothing

### Requirement: Invalid stored value fails closed

If the stored value is not true or false, then the server SHALL refuse to replace the access tables.

#### Scenario: Unknown value refused
- **WHEN** the stored value is not true or false at apply time
- **THEN** the server changes nothing

### Requirement: Effective value visible

When a permitted caller reads the installation settings, the server SHALL answer the stored value of the setting and its default.

#### Scenario: Read shows value
- **WHEN** a permitted caller reads the settings
- **THEN** the answer holds the value and the default

### Requirement: Widened restore audit

When a restore replaces the access tables because the setting is on, the server SHALL write a restore audit entry that names the widened path.

#### Scenario: Widened path recorded
- **WHEN** a widened restore commits
- **THEN** the restore entry names the widened path

### Requirement: Restore keeps the live setting row

While the caller of a restore does not hold server:restore-access, the server SHALL keep the live row of the restore access setting and SHALL NOT take the value from the backup.

#### Scenario: Backup with true, caller without the action
- **WHEN** a backup holds the value true and a caller with only server:restore restores it
- **THEN** the live value stays unchanged

#### Scenario: Backup value, caller with the action
- **WHEN** a caller with server:restore-access restores a backup that holds another value
- **THEN** the server takes the value of the backup and writes a setting-change audit entry with the old and the new value
