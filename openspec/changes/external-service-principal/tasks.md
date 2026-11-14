SECURITY: this change is security code (ADR 250): identity, classification and token exchange. Each layer has its own hostile test, and each guard has a mutation test. Review: a cheap-model review of the change, a Claude review for security, and the Opus review of the stack.

## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 SECURITY. Rule constructor hostile table. Check: NEW test `TestClassificationRuleHostile` in `pkg/principal` fails on main.
- [ ] 1.2 SECURITY. The four values of `existingPrincipals`, a stale digest, the digest of another set, an unknown value, and the rename directions. Check: NEW test `TestReclassifyModes` in the NEW package `internal/principalsvc` fails on main.
- [ ] 1.3 SECURITY. Automated sources never act as a person. Check: NEW test `TestExchangeAutomatedNeverPerson` in `internal/trustrulesvc` fails on main.
- [ ] 1.4 SECURITY. An inactive account gets no token. Check: NEW test `TestExchangeInactiveAccount` in `internal/trustrulesvc` fails on main.
- [ ] 1.5 SECURITY. A service principal gets no console session, MFA or break-glass. Check: NEW test `TestServicePrincipalNoSession` fails on main.
- [ ] 1.6 Conformance cases for the store of the rules and the external key, on both dialects. Check: NEW cases in `pkg/principal/principaltest` fail on main.

## 2. Build

- [ ] 2.1 SECURITY. Add `ClassificationRule`, `Classification`, the external key and the owner to `pkg/principal`, and the baseline columns of each dialect (ADR 322). Check: `go test -race ./pkg/principal/...` passes; the mutation list of each guard is in the pull request.
- [ ] 2.2 SECURITY. Add the classification service with the four values, the digest and the rename rules. Check: `TestReclassifyModes` passes.
- [ ] 2.3 SECURITY. Give the exchange the kind of actor and the active check. Check: `TestExchangeAutomatedNeverPerson` and `TestExchangeInactiveAccount` pass.
- [ ] 2.4 SECURITY. Refuse a console session, MFA and break-glass for a service principal. Check: `TestServicePrincipalNoSession` passes.
- [ ] 2.5 Add the routes with their actions (`server:manage-principals`, `namespace:manage-principals`), `If-Match` on update and delete, and the error codes. Check: the route test shows the actions; `go test ./internal/errcode/...` passes.
- [ ] 2.6 Add the classification, the rule and the owner to the access review export. Check: NEW test `TestAccessReviewListsClassification` passes.

## 3. Side effects

- [ ] 3.1 Regenerate the API description and the clients (`make generate`, `make clients-generate`). Check: the generated files change in the same pull request.

## 4. Definition of done

- [ ] 4.1 E2e: a token exchange of a classified account, a preview, a create with each value of `existingPrincipals`, and a refused console sign-in. Check: `make e2e` passes.
- [ ] 4.2 Docs page `docs/guides/external-service-principals.md`. Check: `make docs-lint` passes, and a test runs each command of the page.
- [ ] 4.3 Usability check: the site administrator completes the path from the docs page alone, and each error names its one command. Check: the check is recorded in the pull request.
- [ ] 4.4 Full check. Check: `make check` reports zero findings.
