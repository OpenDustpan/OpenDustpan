## ADDED Requirements

### Requirement: seed demo workspace objects

When dev starts with demo-admin present on a dev installation, the server SHALL seed one workspace with one state version, one saved view, one approval default, one disabled trigger, and one disabled sink.

#### Scenario: First dev start seeds workspace
- **WHEN** The developer starts dev with an empty database.
- **THEN** The demo namespace holds one workspace with one state version, one saved view, one approval default, one disabled trigger, and one disabled sink.

### Requirement: repair partial seed

When dev starts with some demo objects absent, the server SHALL create only each missing demo object under its demo stable ID.

#### Scenario: Missing sink is recreated
- **WHEN** Dev starts with the demo workspace present but the demo sink absent.
- **THEN** The server creates only the missing sink under its demo stable ID and leaves each other demo object unchanged.

### Requirement: skip without demo-admin

When dev starts with demo-admin absent, the server SHALL seed no workspace object.

#### Scenario: Absent demo-admin skips seeding
- **WHEN** Dev starts with demo-admin absent.
- **THEN** The server seeds no workspace object and writes the demo sign-in output only.
