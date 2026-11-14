## 1. Failing tests first

- [ ] 1.1 Add failing planner tests for settings resolution, override authorization, missing location 422, and Pulumi with no stored steps. Check: TestStartResolvesPoolLocationProfileStepsFromSettings fails on main, passes after the fix.
- [ ] 1.2 Add failing read-check tests for saver loss of unit:read-outputs, missing trigger, disabled saver and manual-fire caller intersection. Check: TestTriggerOutputReadDeniesAfterSaverLosesReadOutputs fails on main, passes after the fix.

## 2. Build

- [ ] 2.1 Wire settings resolution with start snapshot into job start and trigger jobstarter with override checks. Check: `go test ./pkg/dustpan ./pkg/trigger/...` passes with no request-only location fallback.
- [ ] 2.2 Classify unit:run-plan, unit:define-steps, job:read, job:read-plans, job:read-logs and namespace:manage-runners in built-in roles. Check: `go test ./pkg/access/...` shows the planner, lead and administrator role mapping and the route test lists no unclassified start action.
- [ ] 2.3 Wire the saver check into the Pulumi export, OpenTofu state version and secret-output decrypt handlers. Check: Job-token export, state-version and secret-output decrypt paths call OutputReadCheck.Allow at each read.

## 3. Definition of done

- [ ] 3.1 Add the e2e steps against the real CLI and the real directory. Check: e2e step: non-admin with planner role is refused on pool or location override; trigger plan reads source output then is refused after saver loses unit:read-outputs.
- [ ] 3.2 Ship docs, wiring, routes with authorization actions, usability check and make check. Check: `make check` with zero findings and the docs page for unit settings and job start.
