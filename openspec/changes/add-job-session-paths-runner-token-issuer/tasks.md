## 1. Failing tests

- [ ] 1.1 Write failing cases for make at claim, unit check, end on job end, expiry, takeover and retire. Check: go test ./pkg/jobtoken/... -count=1
- [ ] 1.2 Write failing store cases for one token per claim, a race of two claims and a rollback of the claim. Check: go test ./pkg/store/... -run TestJobToken on SQLite and PostgreSQL

## 2. Build

- [ ] 2.1 Implement the issuer with hash storage and unit-limited checks. Check: go test ./pkg/jobtoken/... -count=1

## 3. Definition of done

- [ ] 3.1 Wire the issuer at claim and at job end. Check: go test ./internal/servertest/... -run TestJobTokenWiring
- [ ] 3.2 Add an e2e step that claims a phase and uses the token on its unit and on another unit. Check: make e2e
- [ ] 3.3 Update the docs page runner-api.md and run the usability check. Check: make docs-lint
- [ ] 3.4 Pass the full check. Check: make check with zero findings
