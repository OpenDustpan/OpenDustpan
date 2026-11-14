## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Contract check

- [ ] 1.1 Read each Pulumi changelog and run the e2e suite for 3.274.1, 3.275.0 and 3.276.0 using the driver at `e2e/tool.go:110` and the policy in `e2e/doc.go`. Each run calls ANY /api/stacks/{}/{}/{}/state/{}/resources with an access token through the stored-grant gate. Note login, state, locks, console link and discovery changes in the pull request, or note none found. Confirmed by: CI (make e2e) Check: `E2E_PULUMI_VERSIONS=3.274.1 make e2e`, `E2E_PULUMI_VERSIONS=3.275.0 make e2e`, `E2E_PULUMI_VERSIONS=3.276.0 make e2e`
- [ ] 1.2 Read the OpenTofu changelog and run the e2e suite for 1.13.9 using the driver at `e2e/tool.go:110` and the policy in `e2e/doc.go`. The run calls ANY /api/v2/state-versions with an access token through the stored-grant gate. Note login, state, locks and discovery changes, or note none found. Confirmed by: CI (make e2e) Check: `E2E_TOFU_VERSIONS=1.13.9 make e2e`
- [ ] 1.3 Re-run with contract update enabled and commit each NEW file in the directory `e2e/contract/`. The update run calls ANY /api/stacks/{}/{}/{}/state/{}/resources with an access token through the stored-grant gate. Commit no file from a failed or partial run. Confirmed by: CI (make e2e) Check: `git status --short e2e/contract/` shows the new files committed
- [ ] 1.4 Apply the matrix policy in `e2e/doc.go`. Verification re-runs the pinned versions so each run calls ANY /api/stacks/{}/{}/{}/state/{}/resources and ANY /api/v2/state-versions with an access token through the stored-grant gate. Do not replace a pinned version while any version has an unhandled contract diff. Open a follow-up issue for a diff that needs a server fix. Confirmed by: CI (make e2e) Check: `git diff -- e2e/doc.go`

## 2. Definition of done

- [ ] 2.1 Keep the per-version e2e run log for each of the four versions and confirm against `e2e/contract/` and `e2e/doc.go` that no skipped contract line hides a missing read before commit. The runs call ANY /api/stacks/{}/{}/{}/state/{}/resources and ANY /api/v2/state-versions with an access token through the stored-grant gate. Blocked by e2e-contract-skip-recorded-in until that fix ships. Confirmed by: CI (make e2e) Check: `E2E_PULUMI_VERSIONS=3.276.0 make e2e` plus `E2E_TOFU_VERSIONS=1.13.9 make e2e` with kept run logs
