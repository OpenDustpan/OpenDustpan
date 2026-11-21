## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing adapter tests in NEW file internal/api/tfe/remoteplan_test.go for upload, plan start, argument allowlist, secret variables, signed log read, poll answers, gate refusal, missing pool, local mode, missing grant, cancel and apply refusal. Check: `go test ./internal/api/tfe/ -run TestRemotePlan -count=1` fails on main.
- [ ] 1.2 Add failing service tests in NEW file pkg/job/remoteplan_test.go for the run grant check and the change.propose gates. Check: `go test ./pkg/job/ -run TestRemotePlanGates -count=1` fails on main.

## 2. Build

- [ ] 2.1 Add the e2e step with the real OpenTofu CLI in NEW file e2e/remote_plan_test.go. It uses the existing route /api/v2/workspaces/{} (declared in the route table of its adapter package (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand), adapter in internal/api/tfe/remoteplan.go) to set execution-mode agent and the run routes of the TFE-compatible API, with the access token of a Developer, and a test runner on /runner/v1. Check: `make e2e` passes on SQLite and PostgreSQL over TLS: tofu plan streams the runner log and tofu apply fails with the speculative-only error. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.2 Wire the plan job, the authorization actions and the signed log read in NEW file internal/api/tfe/remoteplan.go, and declare the route in the route table of the adapter package (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description` then writes internal/api/apidesc/openapi.json; never edit it by hand) with its adapter in internal/api/tfe/remoteplan.go. Check: A GET of the signed log-read-url of the run, minted with the access token of a Developer, returns masked chunks and refuses expired and replayed URLs.
- [ ] 2.3 Write the docs page for remote speculative plans in NEW file docs/remote-execution.md. Check: The command blocks of the page run as written in CI. Confirmed by: CI (an e2e step in the NEW file e2e/cli_driven_remote_execution_docs_test.go runs each command of the docs page against the e2e test server).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run make check with zero findings. Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
