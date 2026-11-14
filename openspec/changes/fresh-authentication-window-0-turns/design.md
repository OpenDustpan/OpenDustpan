## Context

Fresh checks guard the steps that ADR 227 names, with one installation window plus narrower namespace and approval-rule windows. Issue 1325 reports a zero window that skips the check, a stale error without the sign-in path, and undecided decrypt scope.

## Decisions

1. **Zero means ask each time.** Fix the window test so 0 asks at each guarded step, including age 0; for nonzero windows age not above the window stays fresh; use the shortest window that holds and refuse a longer narrow window.
2. **Refusal names sign-in path.** Map a stale step to 403 mfa.fresh-step-required with the sign-in path, name the enrollment page when no factor exists, and add a comment on fresh() that names the error code.
3. **Decrypt scope waits for owner.** Make no decrypt change in this change; the owner decides decrypt scope in open questions.
4. **One window and one code.** Keep one window setting from ADR 227 and one error code mfa.fresh-step-required for each guarded route; this change owns both rules and other changes reuse them.

## Structure

Unexported freshChecker built only by newFreshChecker, which accepts only 0 and 60 to 900 and treats 0 as ask-each-time so no caller encodes skip.

## Risks

- Decrypt routes stay without a fresh check until the owner decides their scope.
- A concurrent window change or factor reset during a guarded step can check against a stale window or session generation.
- Clock skew between server and identity provider can make a fresh step look stale.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service compares step age against the shortest window that holds, with 0 as ask-each-time including age 0. | Call a guarded step with window 0 and age 0 and check for 403 mfa.fresh-step-required. |
| HTTP handler | Handler maps a stale step to 403 mfa.fresh-step-required with the sign-in path, and to the enrollment page when no factor exists. | Request a guarded route with a stale session and check that the answer names the sign-in path. |
