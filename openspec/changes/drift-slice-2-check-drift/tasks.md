## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing validator tests in pkg/job/driftsteps_test.go (NEW) for RegisterDriftSteps and ValidateDriftSteps: registered Pulumi steps pass, edited steps, unknown tool and empty table fail. Check: `go test ./pkg/job -run TestValidateDriftStepsPulumi -count=1` NEW test fails on main, passes after.
- [ ] 1.2 Write a failing planner test in pkg/dustpan/jobservice_driftsteps_internal_test.go (NEW): the planned Pulumi steps pass the validator. Check: `go test ./pkg/dustpan -run TestPulumiDriftStepsMatchValidator -count=1` NEW test fails on main, passes after.
- [ ] 1.3 Write the e2e step in e2e/drift_pulumi_test.go (NEW): change a resource out of band, start the check, read drifted, then a clean check reads in-sync. Route POST drift-checks, gate unit:run-plan, access token. Check: `make e2e` runs the NEW step in e2e/drift_pulumi_test.go; it fails on main. BLOCKED by spike 1187 (machine form in RFC 25).

## 2. Build

- [ ] 2.1 Move the OpenTofu argv from pkg/job/driftsteps.go into pkg/job/drift_opentofu.go (NEW), add pkg/job/drift_pulumi.go (NEW) and RegisterDriftSteps, and select the tool key in pkg/dustpan/jobservice.go (func driftSteps). Check: `go test ./pkg/job ./pkg/dustpan ./pkg/runner -count=1` passes; OpenTofu steps still pass.
- [ ] 2.2 Refuse a pulumi start with a purpose other than drift-check in internal/api/jobs/start.go with the error code job.unsupported. Check: `go test ./internal/api/jobs -run TestStartRefusesPulumiNonDrift -count=1` NEW test passes.
- [ ] 2.3 Reduce the Pulumi preview to addresses and kinds in pkg/runner/executor.go and the drift result path, and add the words refresh preview to docs/reference/jobs-api.md. Check: `go test ./pkg/runner -run TestPulumiPreviewReduction -count=1` NEW test passes; BLOCKED by spike 1187 for the machine form.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wiring through POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift-checks (action unit:run-plan, access token), the e2e step in e2e/drift_pulumi_test.go, the docs page, a usability check from the docs page alone, and make check. Check: `make e2e` passes; `make check` reports zero findings.
