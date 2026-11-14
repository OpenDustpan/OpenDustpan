## 1. Failing tests first

- [ ] 1.1 Add failing service tests for chain, expiry with no skew, revocation, revocation unknown, SPIFFE match, single-rule match, hostile case text, and disabled rule or principal. Check: `go test ./pkg/trust/ -run TestMtlsExchangeVerify -count=1` fails on main, passes after the fix.
- [ ] 1.2 Add failing issue tests for stored trust kind, 1-hour expiry setting, last-use record, shared revoke path, atomic write, and concurrent disable. Check: `go test ./pkg/accesstoken/ -run TestTrustTokenIssue -count=1` fails on main, passes after the fix.

## 2. Build

- [ ] 2.1 Build the verified-chain value, single-rule match, stored issue in one transaction, and refusal audit entries. Check: `go test ./pkg/trust/... ./pkg/accesstoken/... -count=1` passes with `-race`.
- [ ] 2.2 Wire the exchange route with its authorization action and direct-TLS placement. Check: `go test ./internal/api/... -run TestMtlsExchangeRoute -count=1` passes and the route test shows the auth action.
- [ ] 2.3 Check the path against a dev server over TLS with a test bundle and rule. Check: `curl --cert` against a dev server with a test bundle gives a token for a known SPIFFE ID and 401 for an unknown ID, a revoked leaf, and no client certificate.

## 3. Definition of done

- [ ] 3.1 Run the full check, the hostile layer tests, and the usability check from the docs draft alone. Check: `make check` reports zero findings.
