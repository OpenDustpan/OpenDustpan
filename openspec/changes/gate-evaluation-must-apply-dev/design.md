## Context

Service.Evaluate returns errNotImplemented today and fails closed by accident. When evaluation lands it must apply the pure approver functions from PR #1119 with the live installation kind read once per evaluation.

## Decisions

1. **One kind snapshot per evaluation.** Evaluate reads the installation kind once at the start through KindReader and uses that snapshot for rule normalization, Unmeetable checks, and signature counting for that evaluation only. Each later decision point reads the kind again.
2. **Fail closed on kind error.** A KindReader error or an unknown kind value denies the evaluation with ErrUnavailable before any rule is checked, so no approval passes without a known kind.

## Structure

Unexported approverPolicy built only by newApproverPolicy(kind installation.Kind) which returns an error on an unknown kind; Service.Evaluate is the only caller and it passes the validated KindReader value.

## Risks

- Evaluation lands after this change and the wiring call site shifts; the failing tests pin the call so a rebase shows the gap.
- Table tests 4a and 4b exist only at pure-function level until Evaluate exists; the Evaluate-level tests close that gap.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Rule normalization | Normalize every stored approval rule, ownership rule, and approval default through RuleApprovers before Unmeetable is checked. | Evaluate a rule with approvers [dev:admin] on production and remove the RuleApprovers call; the test must fail because the rule would look meetable. |
| Unmeetable deny | Deny a requirement that Unmeetable reports as unmeetable, in enforce mode and in evaluate mode. | Evaluate a rule with only NoApprover on production and remove the Unmeetable check; the test must fail because the rule would wait for an approval that nobody can give. |
| Signature filter | Filter every signature through ApproverPolicy.CountsAsSignature and ignore a refused subject in the count. | Evaluate with one dev-principal signature on production and remove the CountsAsSignature filter; the test must fail because the count would reach quorum. |
| Kind fail-closed | Read the installation kind from KindReader once per evaluation; on error return deny without counting signatures. | Make KindReader return an error and bypass the error branch; the test must fail because evaluation would allow with unknown kind. |
