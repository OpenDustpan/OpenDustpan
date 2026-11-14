## ADDED Requirements

### Requirement: lead-notice

When a timed namespace step becomes due within 7 days, the server SHALL record one lead notice for the namespace ID and the target state.

#### Scenario: Seven day warning
- **WHEN** a timed step becomes due within 7 days and no notice exists
- **THEN** the NEW SendLeadNotice path records one notice for the namespace ID and the target state

### Requirement: durable-timed-step

When the due time of a stored timed step arrives, the server SHALL move the namespace to the target state.

#### Scenario: Scheduled step runs
- **WHEN** the due time of a stored timed step arrives and the namespace is still in the source state
- **THEN** the NEW ScheduleTimedStep path moves the namespace to the target state

### Requirement: cancel-timed-step

If the namespace leaves the source state before the due time, then the server SHALL skip the timed step.

#### Scenario: Namespace leaves source state
- **WHEN** the namespace leaves the source state before the due time
- **THEN** the server voids the schedule record and the notice record without a state change

### Requirement: late-notice

If the due time arrives with no prior lead notice, then the server SHALL record the notice at run time and run the step.

#### Scenario: Notice missed before due time
- **WHEN** the due time arrives with no prior lead notice
- **THEN** the server records the notice with a missed mark and then runs the step

### Requirement: notice-visibility

When a timed step is scheduled for a namespace, the server SHALL show the due time and the lead notice time on the read-namespace answer.

#### Scenario: Due time and notice shown
- **WHEN** a timed step is scheduled for a namespace
- **THEN** the read-namespace answer shows the state, the due time and the lead notice time

### Requirement: automatic-step-audit

When an automatic timed step moves a namespace, the server SHALL write one audit entry for the move.

#### Scenario: Step audited
- **WHEN** an automatic timed step moves a namespace
- **THEN** the audit entry names the schedule record, the notice record and the source and target states
