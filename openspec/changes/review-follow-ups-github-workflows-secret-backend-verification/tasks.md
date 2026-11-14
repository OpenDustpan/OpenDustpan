## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test for a secret read through each backend with the same public URL in e2e/upgrade_mixed_test.go. Check: `make mixed-version-test` fails on main before the helper exists. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 Write the failing test that fails when one backend serves no read in e2e/upgrade_mixed_test.go. Check: `make mixed-version-test` fails when the test sends both reads to one listener. Confirmed by: CI (the agent writes the e2e test).

## 2. Definition of done

- [ ] 2.1 Wire the secret round trip into the existing mixed-version run in e2e/upgrade_mixed_test.go and delete the secret at the end. Check: `make mixed-version-test` passes and prints no plaintext. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.2 Run the full gate. Confirmed by: CI (make check). Check: `make check` with zero findings
