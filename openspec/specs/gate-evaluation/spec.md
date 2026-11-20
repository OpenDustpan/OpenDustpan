# gate-evaluation Specification

## Purpose
This capability covers how the server evaluates an approval rule at a gate decision. It reads the installation kind once for each evaluation, keeps dev approvers off a production installation, denies a rule that nobody can meet, and denies the evaluation when it cannot read the kind. It stops at the approver rules: ownership rules and approval defaults at the state-write decision point are not part of it.

## Requirements

### Requirement: Resolve approvers before quorum

When the server evaluates an approval rule, the server SHALL resolve its approvers through RuleApprovers with Production read from KindReader.

#### Scenario: Mixed dev and real approvers on production
- **WHEN** evaluation runs on production with a rule that names one dev subject and one real principal
- **THEN** the requirement stays open to the real approver only

#### Scenario: Empty approver list reads as nobody
- **WHEN** evaluation runs with a rule that names no approvers
- **THEN** the decision denies as unmeetable

#### Scenario: Dev installation keeps dev approvers
- **WHEN** evaluation runs on a dev installation with a rule that names dev subjects
- **THEN** the requirement stays open to the named dev approvers

### Requirement: Deny unmeetable

When a requirement is Unmeetable, the server SHALL deny that requirement.

#### Scenario: Unmeetable denies in enforce mode
- **WHEN** evaluation runs in enforce mode with an unmeetable requirement
- **THEN** the decision denies with zero signatures

#### Scenario: Dev-only rule denies
- **WHEN** evaluation runs on production with a rule that names only dev subjects
- **THEN** the decision denies as unmeetable

### Requirement: Ignore dev signatures on production

When Production is true, the server SHALL ignore each signature that CountsAsSignature refuses.

#### Scenario: Dev signature ignored on production
- **WHEN** a signature from a dev subject arrives on production
- **THEN** the signature fills no slot

#### Scenario: Hostile dev spellings refused
- **WHEN** subjects DEV:admin and bare dev are checked on production
- **THEN** DEV:admin fills no slot and bare dev is treated as the plain subject the rule decides

### Requirement: Fail closed on kind error

If KindReader reports an error or an unknown kind, then the server SHALL deny the evaluation.

#### Scenario: Kind read error denies
- **WHEN** KindReader reports an error
- **THEN** the decision denies without counting a signature

#### Scenario: Unknown kind denies
- **WHEN** KindReader reports a value outside dev and production
- **THEN** the decision denies without counting a signature
