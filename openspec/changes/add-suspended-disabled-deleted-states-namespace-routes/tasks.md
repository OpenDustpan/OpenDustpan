## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing tests in `NEW pkg/namespace/namespace_state_test.go` for suspended read pass and write refusal with namespace.suspended. Check: TestSuspendedWriteRefused in `NEW pkg/namespace/namespace_state_test.go` fails on main, passes after.
- [ ] 1.2 Add failing tests in `NEW pkg/namespace/namespace_state_test.go` for NEW operator SuspendNamespace and ResumeNamespace routes with reason and audit entries in both scopes. Check: TestOperatorSuspendResume in `NEW pkg/namespace/namespace_state_test.go` fails on main, passes after.
- [ ] 1.3 Add failing tests in `NEW pkg/namespace/namespace_state_test.go` for running jobs run to end and disabled and deleted refusals. Check: TestRunningWorkSafe in `NEW pkg/namespace/namespace_state_test.go` fails on main, passes after.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire routes, filter per state, and fenced state check. Check: Route test lists NEW suspend and resume routes with installation:suspend-namespace.
- [ ] 3.2 Add docs page for states and operator action. Check: Docs page renders and commands run as written.
- [ ] 3.3 Run usability check and full checks. Check: `make check` reports zero findings.
