## 1. Failing tests

- [ ] 1.1 Write a failing test for each scenario of the spec Check: go test of the bootstrap package fails on main
- [ ] 1.2 Write the store tests for the admin reset and the challenge use Check: go test of the store conformance suite fails on main

## 2. Implementation

- [ ] 2.1 Add the store method for the admin reset and its audit entry Check: store conformance test for the reset
- [ ] 2.2 Add the marker read to the API Check: route test and API call that returns the marker

## 3. Definition of done

- [ ] 3.1 Wire the host command with its authorization actions Check: route test and `opendustpan host step-up-bootstrap --help`
- [ ] 3.2 Add an e2e step against the real binary Check: `make e2e` passes
- [ ] 3.3 Write the docs page with the sole-key recovery Check: a test runs each command of the page
- [ ] 3.4 Usability check from the docs page alone Check: each error names one command
- [ ] 3.5 Run all checks Check: `make check` has zero findings
