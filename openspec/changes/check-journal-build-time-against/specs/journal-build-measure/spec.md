## ADDED Requirements

### Requirement: finish-build-before-cli-waits-out

When the server completes a create journaled update at the largest accepted size with journaling forced on, the server SHALL finish the journal build before the recorded CLI wait time seen on a stalled complete call.

#### Scenario: build-below-cli-wait
- **WHEN** the server completes one create update at the largest accepted size with journaling forced on and sealed blobs on
- **THEN** the journal build finishes before the recorded CLI wait time and complete returns the checkpoint

#### Scenario: build-past-five-minute-bound
- **WHEN** the journal build runs past 5 minutes
- **THEN** the server refuses the build with journal.build-timeout and stores no new bound

#### Scenario: one-backend-five-repeats
- **WHEN** the timing run starts
- **THEN** the run uses one named local blob backend, sealed blobs on, and 5 repeats, and records the median and the maximum
