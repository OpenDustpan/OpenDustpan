## ADDED Requirements

### Requirement: Resolve approvers before quorum

When the server evaluates an approval rule, an ownership rule, or an approval default, the server SHALL resolve its approvers through RuleApprovers with Production read from KindReader.

#### Scenario: Mixed dev and real approvers on production
- **WHEN** evaluation runs on production with a rule that names one dev subject and one real principal
- **THEN** the requirement stays open to the real approvers and the dev entry counts for nobody

#### Scenario: Dev-only rule normalizes to nobody
- **WHEN** evaluation runs on production with a rule whose approvers normalize to NoApprover
- **THEN** the decision denies and names the unmeetable rule

### Requirement: Deny unmeetable in every mode

When a requirement is Unmeetable, the server SHALL deny that requirement.

#### Scenario: Unmeetable denies in enforce mode
- **WHEN** evaluation runs in enforce mode with a requirement that Unmeetable reports as unmeetable
- **THEN** the decision denies even with zero signatures

#### Scenario: Unmeetable denies in evaluate mode
- **WHEN** evaluation runs in evaluate mode with a requirement that Unmeetable reports as unmeetable
- **THEN** the decision records deny without effect

#### Scenario: Dev-only ownership rule denies
- **WHEN** evaluation runs on production with an ownership rule that names only dev subjects
- **THEN** the decision denies and names the ownership rule

### Requirement: Ignore dev signatures on production

When Production is true, the server SHALL ignore each signature that CountsAsSignature refuses.

#### Scenario: Dev signature ignored on production
- **WHEN** evaluation runs on a production installation with a signature from a dev subject
- **THEN** the signature fills no slot and the requirement stays unmet

#### Scenario: Hostile dev spellings refused
- **WHEN** evaluation runs on production with subjects DEV:admin, a non-ASCII look-alike of dev:, and bare dev
- **THEN** each signature fills no slot

#### Scenario: Ignored dev signature is audited
- **WHEN** evaluation evaluates a signature from a dev subject on production
- **THEN** the audit log holds one entry that names each ignored subject

### Requirement: Use one kind snapshot and fail closed

If KindReader reports an error or an unknown kind, then the server SHALL deny the evaluation.

#### Scenario: Kind read error denies
- **WHEN** KindReader reports an error during evaluation
- **THEN** the decision denies with unavailable and counts no signature

#### Scenario: Unknown kind denies
- **WHEN** KindReader reports a kind value outside dev and production
- **THEN** the decision denies with unavailable and counts no signature

### Requirement: Re-evaluate each decision point

When a job reaches a later decision point, the server SHALL read the installation kind again for that evaluation.

#### Scenario: Kind change after decision
- **WHEN** a restore swap changes the installation kind after a decision was recorded
- **THEN** the next decision point evaluates again with a fresh kind read and does not reuse the stored decision

### Requirement: Hold job on kind failure

When gate evaluation denies for a kind failure, the server SHALL keep the job waiting without applying.

#### Scenario: Job waits on unavailable
- **WHEN** gate evaluation denies with gate.unavailable
- **THEN** the job stays waiting and re-evaluates on the next poll instead of applying or failing

### Requirement: Record approver denials

When the server denies for an unmeetable requirement or a kind failure, the server SHALL write one audit entry that names the reason.

#### Scenario: Unmeetable denial is audited
- **WHEN** evaluation denies an unmeetable requirement
- **THEN** the audit log holds one entry that names the rule and the reason

#### Scenario: Kind-error denial is audited
- **WHEN** evaluation denies because the kind is unknown
- **THEN** the audit log holds one entry that names the kind read failure

#### Scenario: Dropped dev approvers recorded
- **WHEN** evaluation drops dev entries from a mixed rule on production
- **THEN** the audit log holds one entry that names each dropped dev subject

### Requirement: Report stable denial codes

When the server denies for an unmeetable requirement or a kind failure, the server SHALL answer with status 409 and code gate.unmeetable for the unmeetable case and status 503 and code gate.unavailable for the kind case.

#### Scenario: Unmeetable maps to conflict
- **WHEN** evaluation denies an unmeetable requirement
- **THEN** the API answers 409 with code gate.unmeetable

#### Scenario: Kind failure maps to unavailable
- **WHEN** evaluation denies because the kind is unknown
- **THEN** the API answers 503 with code gate.unavailable
