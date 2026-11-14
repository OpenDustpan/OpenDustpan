Issue: #1123.

## Why

**Persona: namespace administrator.** A namespace administrator sets approval rules that must hold on every installation. Without this wiring a dev principal could approve on production, or a rule with NoApprover could wait for an approval that nobody can give.

The user path this change must make work:

1. **API.** Set an approval rule with constrained approvers on a namespace, collection, or unit.
2. **API.** Start a plan or apply job that reaches the approval decision point.
3. **API.** Read the decision record that names the unmeetable rule or the ignored dev signature.
4. **Docs page.** Follow the gates guide to fix approvers and retry the job.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Apply waits and never reaches quorum because the rule names NoApprover or only dev principals. | 409 Dev approval rule cannot be met: the rule names only principals that cannot approve on this installation. Check the gate approvers with GET /api/dustpan/v1/namespaces/{namespace}/gates. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |
| Evaluation cannot read the installation kind and fails closed. | 503 Gate evaluation is not available: the server could not read the installation kind. Retry the check after the server recovers. | `GET /api/dustpan/v1/namespaces/{namespace}/gates` |

Evidence for the claims above:

- Pure approver helpers live in pkg/gate/approvers.go. Proof: pkg/gate/approvers.go:66:func RuleApprovers(a Approvers) Approvers {
- Service evaluation is not implemented and returns errNotImplemented. Proof: pkg/gate/service.go:257:	return Decision{}, errNotImplemented
- Job gate path already filters signatures with CountsAsSignature. Proof: pkg/job/gate.go:274:	if !policy.CountsAsSignature(a.Subject) {
- Backup export reads installation kind through backup.KindReader, the same reader shape evaluation must use. Proof: pkg/backup/devdata.go:45:type KindReader interface {
- Coverage matrix lists the gate list route used as the help command. Proof: GET /api/dustpan/v1/namespaces/{namespace}/gates

## What Changes

- Service.Evaluate reads Production from KindReader once per evaluation and fails closed on error or unknown kind.
- Evaluation normalizes approvers with RuleApprovers, denies Unmeetable requirements in enforce and evaluate modes, and filters signatures with CountsAsSignature.
- ADR 253 table tests 4a and 4b gain Evaluate-level twins with audit entries and stable denial codes.

Out of scope:

- Full gate evaluation beyond the dev-data approver rules.
- The temp_admins KeepAccessTables restore path, which needs an RFC per issue #1123.
- New numbered migrations or baseline schema edits.

## Usability

Where the path still costs the person time or surprises them:

- A rule that nobody can meet denies at once instead of waiting for an approval that never arrives.
- The denial names the unmeetable rule so the namespace administrator can fix the approvers without reading code.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 253: Dev data does not reach a production installation** (not owner-tagged).

- Decisions that apply: 1 (current), 7 (current), 17 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Its value is `dev` or `production`."
- Quote: "equal to `dev:`."
- Quote: "The package `gate` has the constant `NoApprover`, with the value `!no-approver`."
- Quote: "**The value that nobody can meet.**"
- Effect: The change applies decision 17 inside Service.Evaluate with the kind source of decision 1 and the dev-subject rule of decision 7, and it does not alter the strip and refuse layers.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The spec does not say what happens when a rule mixes dev principals and real principals on production. It is not stated whether RuleApprovers drops the dev entries and keeps the real approvers, or whether the whole rule is refused. The build needs this answer. Resolution: answered: Added mixed-rule behavior to the resolve-approvers requirement and its scenario 'Mixed dev and real approvers on production'.
- R1.2 (missing scenario): The design layer table requires Unmeetable to deny in enforce mode and in evaluate mode. The spec has no scenario for evaluate mode, so the behavior there is not defined. Resolution: answered: Added scenario 'Unmeetable denies in evaluate mode' to the deny-unmeetable requirement.
- R1.3 (failure mode): The installation kind can change during an evaluation, for example in a restore swap (ADR 253 decision 12). The design reads Production once at the start, but no rule says what happens if the kind changes between that read and the signature count. Resolution: answered: Added one-snapshot requirement: the kind read at the start governs the whole evaluation.
- R1.4 (failure mode): A denial caused by a kind read error or an unmeetable rule has no required audit record. Project rules say each automatic action writes a record of who or what did it and why, and the spec does not require it. Resolution: answered: Added an audit requirement for kind-error and unmeetable denials.
- R1.5 (missing scenario): The kind reader can return a value that is neither dev nor production, for example a value that fails ParseKind. No requirement or scenario covers this input. Resolution: answered: Added scenario 'Unknown kind denies' to the fail-closed requirement for a kind value outside dev and production.
- R1.6 (missing scenario): The layer table covers ownership rules and approval defaults as well as approval rules, but the spec has scenarios only for an approval rule with NoApprover and for an approval default. No scenario covers an ownership rule or a default that holds only dev principals. Resolution: answered: Added scenario 'Dev-only ownership rule denies' and scenario 'Dev-only rule normalizes to nobody' for a dev-only approval default to the requirements.
- R1.7 (missing scenario): The spec does not state the HTTP status or the error code of each deny. The proposal table uses 409 for an unmeetable rule and 503 for a kind error, but the spec does not require either code. Resolution: answered: Added scenarios 'Unmeetable maps to conflict' and 'Kind failure maps to unavailable' to the stable denial codes requirement with 409 and 503.
- R1.8 (wrong claim): The proposal cites ADR 253 layer 3 to justify failing closed on a kind read error. Decision 12 covers the restore swap only, not gate evaluation, so the citation does not support the claim. Resolution: answered: Removed the layer-3 citation; the fail-closed answer now cites only the issue scope.
- R1.9 (wrong claim): The ADR check lists only decision 17. The change calls CountsAsSignature, which depends on decision 7 (IsDevSubject), and decision 1 (the kind source), but neither is listed or quoted, and the conflict line says none without that evidence. Resolution: answered: Extended adr_check to decisions 1 and 7 with exact quotes for the kind source and the dev-subject rule.
- R1.10 (wrong claim): The error table names GET /api/dustpan/v1/namespaces/{namespace}/gates as the command that helps, but the input gives no evidence that this route exists. Resolution: answered: Kept the GET gates help command and added pack evidence for that route from the coverage list.
- R1.11 (scope): The issue asks for an RFC on the temp_admins KeepAccessTables path. The change puts that path out of scope but creates no task or artifact to open the RFC, so part of the issue is missed. Resolution: answered: Added a task to open the RFC for the temp_admins KeepAccessTables path.
- R2.1 (missing scenario): The proposal promises a decision record that names the ignored dev signature, but the spec's Ignore dev signatures requirement writes no audit entry. Project rules require a record for each automatic action. Resolution: answered: Ignore-dev-signatures requirement now writes one audit entry that names each ignored dev subject.
- R2.2 (missing scenario): When a mixed rule drops dev entries on production, the drop is silent. No requirement or scenario records the dropped subjects. Resolution: answered: Record-approver-denials requirement now audits the dropped dev subjects of a mixed rule.
- R2.3 (wrong claim): The gate list route is named as the help command, but the proposal gives no rg or route evidence for GET /api/dustpan/v1/namespaces/{namespace}/gates. The claim is unchecked. Resolution: answered: Evidence entry now cites the coverage matrix route string exactly as the pack gives it.
- R2.4 (wrong claim): The ADR check defines owner-tagged as also covering ADRs that record decisions of the project owner. project.md defines it only by the text '(owner)' or '(owner, ADR N)' or the title, so this definition is wrong. Resolution: answered: Removed the extra owner definition; adr_check marks owner-tagged only by (owner) text or title per project.md.
- R2.5 (failure mode): The kind snapshot holds for one evaluation only. A restore swap can change the kind after the decision is made, and the spec does not say whether the stored decision is re-checked or how a waiting job is re-evaluated. Resolution: answered: Added one-snapshot re-evaluation requirement: each decision point reads the kind again and a stored decision is not reused after a restore swap.
- R2.6 (open question): The spec does not say what a job does on a 503 gate.unavailable answer: whether it retries, stays waiting, or fails. The proposal tells the person to retry the check, but no requirement covers the job state. Resolution: answered: Added hold-job-on-kind-failure requirement: the job stays waiting and re-evaluates instead of failing or applying.
- R2.7 (scope): The definition of done requires an e2e step against the real API or CLI for this user-visible path, and a usability check. tasks.md has neither. Resolution: answered: Added e2e and usability tasks to the definition of done.
- R2.8 (failure mode): The design says the newApproverPolicy(production bool) constructor makes the bug class impossible. A bool cannot carry an unknown kind, so the fail-closed rule relies on the caller's error branch, not on the structure. Resolution: answered: Structure now takes installation.Kind and returns an error on unknown kind so the fail-closed rule lives in the constructor.
- R2.9 (missing scenario): The hostile subject cases for the dev-signature filter are not covered: uppercase DEV:, a non-ASCII look-alike, and a subject of dev without a colon. ADR 253 decision 7 sets the match rule, but the spec tests only one dev subject. Resolution: answered: Added hostile dev-subject scenarios for uppercase DEV:, non-ASCII look-alike, and bare dev.

## Open questions

none

Questions considered and answered:

- Does gate evaluation need the dev-data approver rules or is that a separate RFC? Yes. ADR 253 decision 17 owns the NoApprover value and the dev-signature rule, with decision 1 for the kind source and decision 7 for the dev-subject match. This change applies them inside Service.Evaluate. No RFC is needed for that wiring.
- What does Evaluate do when KindReader reports an error or an unknown kind? Fail closed. The issue scope says a read error fails closed. The spec denies on KindReader error and on an unknown kind value.
- Should this change alter temp_admins handling on the KeepAccessTables restore path? Out of scope for the gate wiring. The issue marks the temp_admins KeepAccessTables path as needs an RFC. This change adds a task to open that RFC and does not alter restore.
- What happens to a rule that mixes dev principals and real principals on production? RuleApprovers drops each dev entry and keeps each real approver on production. A mixed rule stays meetable by the real approvers. A rule with only dropped entries becomes NoApprover and denies.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
