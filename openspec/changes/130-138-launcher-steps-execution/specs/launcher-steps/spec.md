## ADDED Requirements

### Requirement: StartStep no-shell start

When a launcher starts a checked step with StartStep, the launcher SHALL start the program with the argument list and no shell.

#### Scenario: StartStep without shell
- **WHEN** a checked step starts with StartStep
- **THEN** the launcher starts the program with the argument list and no shell.

### Requirement: StartStep bounds

When a step breaks the argument bounds, the launcher SHALL refuse the step with StartStep.

#### Scenario: refuse empty list
- **WHEN** a step has zero arguments
- **THEN** the launcher refuses the step with the NEW error code `launcher.step-invalid`.

#### Scenario: refuse too many or too large
- **WHEN** a step has more than 256 arguments or more than 131072 bytes in all or a NUL byte
- **THEN** the launcher refuses the step with the NEW error code `launcher.step-invalid`.

#### Scenario: refuse bad program
- **WHEN** a step program is neither a bare name nor a clean absolute path
- **THEN** the launcher refuses the step with the NEW error code `launcher.step-invalid`.

### Requirement: ReportExit once

When a step ends, the launcher SHALL report one exit code with ReportExit under its claim.

#### Scenario: ReportExit stores one code
- **WHEN** a step ends with ReportExit
- **THEN** the server stores one exit code for the step.

#### Scenario: ReportExit after restart
- **WHEN** the launcher restarts before it reports the exit
- **THEN** the launcher reads the local step state file and reports the stored exit once with no second start.

#### Scenario: ReportExit duplicate refused
- **WHEN** a launcher reports an exit twice for one step
- **THEN** the server keeps the first exit code and refuses the second report.

#### Scenario: ReportExit wrong claim denied
- **WHEN** an exit report carries a handoff token from another claim
- **THEN** the server denies the report with code `runner.retired`.

#### Scenario: ReportExit signal kill mapped
- **WHEN** a step process ends by signal termination
- **THEN** the launcher reports exit code 143 for the killed step.

### Requirement: stream ordered chunks

While a step runs, the launcher SHALL stream output as ordered chunks of at most 65536 bytes at a rate of at most 100 chunks per second per step.

#### Scenario: stream in order
- **WHEN** a step writes output
- **THEN** the server stores chunks in sequence order with no gap.

#### Scenario: resume from next sequence
- **WHEN** the log stream breaks mid-step
- **THEN** the launcher resumes from the stored next sequence number with no repeat.

#### Scenario: refuse oversize chunk
- **WHEN** a log chunk holds more than 65536 bytes
- **THEN** the server refuses the chunk with code `runner.limit`.

### Requirement: claim check at use

When the server gets a stream chunk or an exit report, the server SHALL check that the handoff token owns the claim.

#### Scenario: deny stream on lost claim
- **WHEN** a stream chunk carries a handoff token with no claim
- **THEN** the server denies the chunk with code `runner.retired` and stops the session.

#### Scenario: audit denial
- **WHEN** the server denies a stream chunk or an exit report
- **THEN** the server writes an audit entry with the claim, the step, and the denial reason.

### Requirement: validate local file

When a runner operator runs the NEW command `opendustpan job steps validate` on a steps file, the command SHALL check the file locally with no server call.

#### Scenario: validate good file passes
- **WHEN** the file holds a valid steps list
- **THEN** the command prints the step count and exits 0.

#### Scenario: validate bad file fails
- **WHEN** the file holds a bad bound or a bad program form or malformed JSON or a missing file
- **THEN** the command prints one machine-readable error per bad step with code `launcher.step-invalid` and exits 2.
