## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a test in the NEW package internal/triggersvc that builds the manager with NewSaverCheck and NewOutputReadCheck from that package. Check: `go test ./internal/triggersvc/...` fails on main because the package does not exist

## 2. Move

- [ ] 2.1 Move the Manager, the dispatcher, the saver check, OutputReadCheck and jobstarter to the NEW package internal/triggersvc. Keep Trigger, the ports, the sentinels, CheckCycle and triggertest in pkg/trigger. The tests of the moved code call the existing route POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire with an access token and pass the same authorization gate as before. Check: `rg -n -F 'NewOutputReadCheck' pkg` and `rg -n -F 'NewSaverCheck' pkg` print nothing
- [ ] 2.2 Rewire newTriggerManager in pkg/dustpan/triggers.go to internal/triggersvc and fix the pkg/dustpan tests. Check: `go test ./pkg/dustpan/...` passes, including TestNewTriggerManagerReadsFinalVersionFromJobStore
- [ ] 2.3 Add internal/triggersvc to the domain rule of .golangci.yml. Check: `golangci-lint run ./...` reports nothing

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the trigger e2e step with the real CLI. It calls the existing route POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire with an access token and passes the same authorization gate as before. Docs pages need no change. Confirmed by: CI (make e2e). Check: `make e2e` passes
- [ ] 4.2 Run the full check. The route table and the API description do not change. Confirmed by: the driver's gates. Check: `make check` with zero findings
