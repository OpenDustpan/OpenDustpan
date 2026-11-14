## 1. Failing tests

- [ ] 1.1 Add failing e2e and service tests for planner state read with Pulumi CLI and OpenTofu, including masked read of a state version written before the change. Check: TestPlannerReadsStateWithBothCLIs fails on main.
- [ ] 1.2 Add failing tests for refused up, destroy, apply, secret reveal, state history export, non-plan job start, token creation for another principal, grant union and revocation. Check: TestPlannerWriteRefused, TestPlannerSecretRevealRefused, TestPlannerOtherRoutesRefused, TestPlannerGrantUnion and TestPlannerRevocation fail on main.

## 2. Implementation

- [ ] 2.1 Define the planner action set as viewer plus unit:run-plan, job:read-plans and changeorder:author with no secret action. Check: `go test ./pkg/access/...` passes with the planner action set including changeorder:author.
- [ ] 2.2 Wire the action checks into Pulumi, TFE-compatible, HTTP backend and tool-neutral handlers for reads, writes, reveals, exports, settings, token and job routes. Check: `go test ./internal/api/...` shows 403 unit.forbidden for planner writes and 403 secret.forbidden for planner reveals, plus 403 for settings, token, export and non-plan job routes.
- [ ] 2.3 Enforce the action check and read-time masking in store adapters. Check: `go test ./pkg/store/...` shows the adapter refuses a planner write in the fenced transaction and masks secrets at read time for all state versions.
- [ ] 2.4 Close the server plan-job start for planners pending the protected-branch gate. Check: `go test ./internal/api/...` shows 403 for planner plan-job start until the milestone 5 gate ships.
- [ ] 2.5 Run the e2e planner scenario with a planner token for the local CLI path only. Check: Planner scenario with the real Pulumi CLI and OpenTofu passes for local read, local plan and preview with masked secrets, refused write and refused reveal.

## 3. Definition of done

- [ ] 3.1 Update the docs page with grant, read, refused write and masked local plan examples. Check: Planner role page renders and each command runs as written.
- [ ] 3.2 Run a usability check of the planner path. Check: Developer completes the local CLI path from the docs page alone and each error names the help command.
- [ ] 3.3 Run the full check including route, policy and audit tests. Check: `make check` reports zero findings.
