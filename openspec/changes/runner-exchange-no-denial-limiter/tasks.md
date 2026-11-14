## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests and measurement first

- [ ] 1.1 Write the failing test for the bad-proof audit entry in NEW pkg/runner/exchange_denial_test.go. Confirmed by: CI (make check). Check: TestExchangeBadProofWritesReasonClassOnly in NEW pkg/runner/exchange_denial_test.go fails on main. It calls the exchange route with a trust token and reads the audit entries route with an access token.
- [ ] 1.2 Write the failing test for the denial limit in NEW pkg/runner/exchange_denial_test.go. Confirmed by: CI (make check). Check: TestExchangePassingProofFloodIsLimited in NEW pkg/runner/exchange_denial_test.go fails on main. It calls the exchange route with a trust token and reads the audit entries route with an access token.
- [ ] 1.3 Record the nonce row cost against the limiter cost for the exchange denial path in NEW pkg/runner/exchange_denial_bench.txt. Confirmed by: CI (make check). Check: NEW file pkg/runner/exchange_denial_bench.txt exists with the nonce row cost and `make check` passes.

## 2. Implementation

- [ ] 2.1 Write the reason-class audit entry in NEW pkg/runner/exchange_denial.go. Confirmed by: CI (make check). Check: TestExchangeBadProofWritesReasonClassOnly passes for the handler in NEW pkg/runner/exchange_denial.go.
- [ ] 2.2 Enforce the limit and the suppressed summary in NEW pkg/runner/exchange_denial.go. Confirmed by: CI (make check). Check: TestExchangePassingProofFloodIsLimited passes for the handler in NEW pkg/runner/exchange_denial.go.

## 3. Definition of done

- [ ] 3.1 Add an e2e step for a forged exchange that calls ANY/POST /api/dustpan/v1/namespaces/{}/trust-rules/exchange with a trust token. Confirmed by: CI (e2e job). Check: The e2e step calls ANY/POST /api/dustpan/v1/namespaces/{}/trust-rules/exchange with a trust token and passes in CI. Confirmed by: CI (e2e job).
- [ ] 3.2 Document the limit and the audit entry in NEW docs/reference/runner-exchange.md. Confirmed by: CI (make check). Check: The page gives 30, per 60 seconds, per client address, namespace and rule.
- [ ] 3.3 Run the usability check of the refusal path. A person's step. Check: A person completes the path from the docs page alone. A person's step.
- [ ] 3.4 Run the definition of done. Confirmed by: CI (make check). Check: `make check` with zero findings. Confirmed by: CI (make check).
