## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write a method test in the NEW file internal/signinsvc/ldap_deadline_test.go that stalls the rebind for 11 s and expects the refusal inside the 30 s deadline. Check: TestLDAPGroupDeadlineCoversRebind fails on main: the refusal comes after the deadline plus the rebind time
- [ ] 1.2 Write a route test in the NEW file internal/api/server/signin_password_reason_test.go that forces checkEnabled and bind gate errors with text and checks the fixed answers. It calls the public route POST /api/dustpan/v1/sign-in/providers/{provider}/password with no credential. Check: TestProviderPasswordRefusalsAreFixed fails on main: the answer holds the error text
- [ ] 1.3 Write the binary e2e in the NEW file e2e/ldap_sign_in_binary_test.go. It calls the public route POST /api/dustpan/v1/sign-in/providers/{provider}/password with no credential. Check: TestLDAPSignInBinary fails on main: no test starts the binary with the LDAP flags. Confirmed by: CI (the agent writes the e2e test).

## 2. Fix

- [ ] 2.1 Start the group deadline before the rebind in internal/signinsvc/password.go and give the rebind 10 s. Check: go test ./internal/signinsvc/... passes
- [ ] 2.2 Map the errors to fixed reasons in the provider password handler under internal/api/server. Check: go test ./internal/api/server/... passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the binary e2e step against the real binary and ldaptest. It calls the existing public route POST /api/dustpan/v1/sign-in/providers/{provider}/password, which has no gate and needs no credential. Check: make e2e. Confirmed by: CI (make e2e)
- [ ] 4.2 Update the LDAP sign-in docs page with the flags, the password file, the allowed ranges and the refusal text. Check: make docs-lint. Confirmed by: CI (make docs-lint)
- [ ] 4.3 The site administrator completes the LDAP path from the docs page alone, and each error names the one command that helps. Check: A person checks the path. A person's step.
- [ ] 4.4 Run the full check. Check: `make check` with zero findings. Confirmed by: CI (make check)
