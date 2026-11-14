## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the NEW test `TestFanoutStartsOnePlanJobPerUnit` for 3 units and show that it fails on main. Check: the NEW test `TestFanoutStartsOnePlanJobPerUnit`
- [ ] 1.2 Write the NEW test `TestFanoutApplyBindsOneWorkspace` for lock, state version, and history entry isolation and show that it fails on main. Check: the NEW test `TestFanoutApplyBindsOneWorkspace`
- [ ] 1.3 Write the NEW test `TestFanoutStopsOnFirstFailure` for failed plan, failed apply, and new revision cancel and show that it fails on main. Check: the NEW test `TestFanoutStopsOnFirstFailure`

## 2. Implementation

- [ ] 2.1 Implement fan-out of plan jobs through the existing wiring with stable unit IDs, start key `<change order ID>/<revision>`, no retry of a finished job, and allowlist of operations `plan` and `apply`. Check: the NEW test `TestFanoutStartsOnePlanJobPerUnit`
- [ ] 2.2 Implement apply fan-out only for units whose plan has a change, with per-unit `unit:run-apply` or `unit:run-destroy` checks, each apply spec naming its stored plan ID, and each claim owning its workspace lock resource which it opens and closes. Check: the NEW test `TestFanoutApplyBindsOneWorkspace`
- [ ] 2.3 Implement stop on first failure with cancel of pending apply jobs, `plan-failed` on failed or empty plans, and cancel of prior revision plan jobs on attach. Check: the NEW test `TestFanoutStopsOnFirstFailure`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add the e2e step BLOCKED by the missing change order fan-out path: over 3 workspaces it checks 3 plan jobs and 3 apply jobs, each on its own unit, each with 1 new state version and 1 history entry. Check: e2e step BLOCKED by the missing change order fan-out path over 3 workspaces with an access token
- [ ] 4.2 Add the guide for the fan-out record and the gated apply. Check: add the new file `docs/guides/terragrunt-run-all.md`
- [ ] 4.3 Run the usability check with the Runner operator from the docs page alone and record where the path costs time. Check: usability walkthrough with the Runner operator from the docs page alone
- [ ] 4.4 Run `make check` with zero findings. Check: `make check` with zero findings
