## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing service tests for the verify scenarios (NEW test TestMtlsExchangeVerify). Check: `go test ./pkg/trust/ -run TestMtlsExchangeVerify` fails on main
- [ ] 1.2 Write failing issue tests (NEW test TestMtlsTrustTokenIssue). Check: `go test ./pkg/accesstoken/ -run TestMtlsTrustTokenIssue` fails on main

## 2. Build

- [ ] 2.1 Build the verified-chain value, match and one-transaction issue. Check: `go test -race ./pkg/trust/... ./pkg/accesstoken/...`

## 3. Definition of done

- [ ] 3.1 BLOCKED by open question 1 (owner decision on kind trust for mTLS) and by part 1 (bundle and rule API); then wire the route POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange with its action. Check: Route test shows the action; `make check` with zero findings
- [ ] 3.2 Usability check from the docs draft. Check: The persona completes the path and each error names one command
