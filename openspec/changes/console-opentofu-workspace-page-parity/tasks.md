## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test for the maintainer 403 on force-unlock in NEW internal/api/tfe/force_unlock_test.go. Check: TestForceUnlockMaintainer403 calls ANY /api/v2/workspaces/{}/actions/force-unlock with an access token and expects 403
- [ ] 1.2 Write the failing test for the missing reason on the console route in NEW internal/api/inventory/force_unlock_reason_test.go. Check: TestForceUnlockReasonRequired calls ANY /api/v2/workspaces/{}/actions/force-unlock with an access token and expects 422

## 2. Server work

- [ ] 2.1 Audit G1 to G12 against the coverage matrix in internal/api/tfe/workspaces.go:459 (func Handler.listWorkspacePage) and open one issue per open gap. Check: No route: the test compares the coverage matrix against the issue list only and writes the result.
- [ ] 2.2 Enforce the NEW reason and the audit entry in pkg/manuallock/service.go:168 (func Service.ForceUnlock). Check: TestForceUnlockReasonRequired calls ANY /api/v2/workspaces/{}/actions/force-unlock with an access token

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add the e2e step for force-unlock across the OpenTofu matrix with the real CLI in e2e/tool.go:110 (func OpenTofu). Confirmed by: CI (e2e job) Check: `make e2e` drives tofu force-unlock on ANY /api/v2/workspaces/{}/actions/force-unlock with an access token across the OpenTofu matrix. Confirmed by: CI (e2e job)
- [ ] 4.2 Add the docs page for the workspace reads and force-unlock. Check: No route: the test reads the new docs page only
- [ ] 4.3 Check the handoff for phone-width on-call use with pkg/dustpan/console_control_test.go:14. Check: No route: the test follows the docs page and checks each error names one command only
- [ ] 4.4 Run make check. Confirmed by: CI (make check) Check: `make check` reports zero findings. Confirmed by: CI (make check)
