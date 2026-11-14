## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing service test that an agent confirm through delegation and with own token gets 403 changeorder.agent-confirm-refused. Check: TestAgentConfirmRefused fails on main, passes after fix.
- [ ] 1.2 Add failing service test that delegator confirm plus approval does not satisfy authorMayApprove false, and passes with authorMayApprove true. Check: TestDelegatorIsAuthorUnderTwoPersonRule fails on main, passes after fix.
- [ ] 1.3 Add failing service test that a new revision after confirmation needs a new person confirmation and that a confirm race against a new revision is refused. Check: TestConfirmBindsRevision fails on main, passes after fix.
- [ ] 1.4 Add failing service test that apply and destroy starts are refused with changeorder.not-ready while not approved. Check: TestNoApplyWhileProposed fails on main, passes after fix.
- [ ] 1.5 Add failing service test that confirm with an access token is refused with mfa.fresh-step-required when a fresh step is needed. Check: TestConfirmNeedsFreshStep fails on main, passes after fix.
- [ ] 1.6 Add failing service test that revoke after proposal keeps the delegator as author. Check: TestRevokedDelegationKeepsAuthor fails on main, passes after fix.

## 2. Wiring and docs

- [ ] 2.1 Wire the NEW confirm route with the NEW action changeorder:confirm and add an e2e step in the NEW file e2e/change_order_confirm_test.go. Check: The e2e step calls the NEW route POST /api/dustpan/v1/namespaces/{namespace}/change-orders/{id}/confirm with a session credential of the person, then GET /api/dustpan/v1/namespaces/{namespace}/approval-rules; BLOCKED by the change-order routes of the item dusty-proposes-change-order.
- [ ] 2.2 Add namespace-admin step that sets authorMayApprove and checks four-eyes. Check: PUT /api/dustpan/v1/namespaces/{namespace}/approval-rules/{id} with a session credential sets authorMayApprove false; the delegator second approval is refused.
- [ ] 2.3 Add the NEW error codes changeorder.agent-confirm-refused and changeorder.not-ready to the error catalog. Check: The route test checks the 403 and 409 shapes.
- [ ] 2.4 Write the docs page section for person confirmation of agent-proposed change orders. Check: Read the new confirm section of docs/guides/approval-rules.md and run each step as written.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the e2e route check for confirm then apply. Check: GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job} with a session credential shows no apply step before confirmation; BLOCKED by the change-order routes of dusty-proposes-change-order.
- [ ] 4.2 Run the e2e audit check for no apply before confirmation. Check: GET /api/dustpan/v1/namespaces/{namespace}/audit (NEW) with a session credential of a namespace administrator shows the confirmation entry before any apply entry; BLOCKED by the audit export route of every-step-audited.
- [ ] 4.3 Run the usability check of the confirm path. Check: The persona completes confirm from the docs page alone and each error names the one command that helps.
- [ ] 4.4 Run the full check suite. Check: `make check` with zero findings.
