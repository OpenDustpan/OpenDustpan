## 1. Failing tests first

- [ ] 1.1 Write failing tests for a direct grant, a group grant and a member add, each with a covering and a non-covering caller. Check: go test ./pkg/access/... ./pkg/principal/... fails on main
- [ ] 1.2 Write failing tests for the flag of a widened edge, its acknowledge, its revoke, its end with the edge, and a caller without cover. Check: go test ./pkg/access/... fails on main
- [ ] 1.3 Write failing conformance tests for concurrent grant, member add and revoke, on both store adapters. Check: go test -timeout 60m ./pkg/store/... fails on main
- [ ] 1.4 Write failing tests for token create and rotation to a protected member, and for an unprotected member. Check: go test ./pkg/accesstoken/... fails on main
- [ ] 1.5 Write a failing test that account-administrator reach holds no operator-family action. Check: go test ./pkg/access/... fails on main

## 2. Implementation

- [ ] 2.1 Add the cover proof and the checks in the grant service, AddMember and the token issue, with the flag record and its end rule. Check: go test ./pkg/access/... ./pkg/principal/... ./pkg/accesstoken/... passes
- [ ] 2.2 Map the sentinels to 403 and add the flag routes with their authorization actions and the API description. Check: go test ./internal/api/server/... passes, and the route test lists each action

## 3. Definition of done

- [ ] 3.1 Add e2e steps for the refused grant, member add, token create and rotation, and the flagged edge with acknowledge and revoke. Check: make e2e passes
- [ ] 3.2 Write the docs page and walk the user path from it. Check: make docs-lint passes, and each error names its one command
- [ ] 3.3 Run the full check. Check: make check with zero findings
